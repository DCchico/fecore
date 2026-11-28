package proxy

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.gatech.edu/faasedge/fecore/pkg/provider/handlers"
)

type stubResolver struct {
	target url.URL
	delay  time.Duration
	onCall func()
}

func (s *stubResolver) Resolve(functionName string, requestID string, dependencies []handlers.DependencyInfo, vDAGID string, sequenceID string, releaseLocalState bool, reserveLocalState bool, dataCentricDependency string, ctx context.Context) (url.URL, bool, bool, error) {
	if s.delay > 0 {
		timer := time.NewTimer(s.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return url.URL{}, false, false, ctx.Err()
		case <-timer.C:
		}
	}
	if s.onCall != nil {
		s.onCall()
	}
	return s.target, false, true, nil
}

func withHealth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		h(w, r)
	}
}

func dispatch(t *testing.T, resolver *stubResolver, fs *handlers.FunctionStore, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if req == nil {
		req = httptest.NewRequest(http.MethodPost, "/function/fn", bytes.NewReader([]byte(`{}`)))
	}
	req = mux.SetURLVars(req, map[string]string{"name": "fn"})
	rec := httptest.NewRecorder()
	proxyRequest(rec, req, &http.Client{}, resolver, fs)
	return rec
}

func TestClientAbortThenNormalCompletionReleases(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	req := httptest.NewRequest(http.MethodPost, "/function/fn", bytes.NewReader([]byte(`{}`)))
	ctx, stop := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	go func() {
		<-started
		stop()
	}()
	rec := dispatch(t, &stubResolver{target: *target}, fs, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls %d", calls.Load())
	}
	active, idle, replicas := fs.ReplicaSets("fn")
	if active != 0 || idle != 1 || replicas != 1 {
		t.Fatalf("completed abort active=%d idle=%d replicas=%d", active, idle, replicas)
	}
}

func TestTruncated500DoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = buf.WriteString("HTTP/1.1 500 Internal Server Error\r\nContent-Length: 100\r\n\r\nnope")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	rec := dispatch(t, &stubResolver{target: *target}, fs, nil)
	if calls.Load() != 1 {
		t.Fatalf("retried a truncated 500: calls %d", calls.Load())
	}
	active, idle, replicas := fs.ReplicaSets("fn")
	if active != 0 || idle != 0 || replicas != 1 {
		t.Fatalf("truncated 500 active=%d idle=%d replicas=%d status=%d", active, idle, replicas, rec.Code)
	}
}

func TestEmpty500WithASideEffectIsNotReplayed(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	rec := dispatch(t, &stubResolver{target: *target}, fs, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if posts.Load() != 1 {
		t.Fatalf("application posts %d", posts.Load())
	}
	active, idle, _ := fs.ReplicaSets("fn")
	if active != 0 || idle != 1 {
		t.Fatalf("after the single response active=%d idle=%d", active, idle)
	}
}

func TestFunctionPortDialIsTheReadinessGate(t *testing.T) {
	ctx := context.Background()
	ready, retry := invocationReady(ctx, url.URL{Host: "127.0.0.1:9"})
	if !ready || !retry {
		t.Fatalf("non-watchdog address ready=%t retry=%t", ready, retry)
	}
	probe, err := net.DialTimeout("tcp", "127.0.0.1:5000", 50*time.Millisecond)
	if err == nil {
		_ = probe.Close()
		t.Skip("port 5000 is already accepting")
	}
	ready, retry = invocationReady(ctx, url.URL{Host: "127.0.0.1:" + watchdogPort})
	if ready || !retry {
		t.Fatalf("closed function port ready=%t retry=%t", ready, retry)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:5000")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, accErr := ln.Accept()
			if accErr != nil {
				return
			}
			_ = c.Close()
		}
	}()
	ready, retry = invocationReady(ctx, url.URL{Host: "127.0.0.1:" + watchdogPort})
	if !ready || !retry {
		t.Fatalf("open function port ready=%t retry=%t", ready, retry)
	}
}

func TestDirectPythonListenerIsReadyWithoutTheWatchdogUpstream(t *testing.T) {
	if probe, err := net.DialTimeout("tcp", "127.0.0.1:5000", 50*time.Millisecond); err == nil {
		_ = probe.Close()
		t.Skip("port 5000 is already accepting")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skip("port 8080 is already accepting")
	}
	watchdog := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "")
		w.WriteHeader(http.StatusOK)
	})}
	go watchdog.Serve(ln)
	waitForLocalAccept(t, "127.0.0.1:8080")
	ready, retry := invocationReady(context.Background(), url.URL{Host: "127.0.0.1:8080"})
	watchdog.Close()
	if ready || !retry {
		t.Fatalf("watchdog health without its upstream ready=%t retry=%t", ready, retry)
	}
	ln, err = net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	python := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "BaseHTTP/0.6 Python/3.11.18")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	})}
	go python.Serve(ln)
	defer python.Close()
	waitForLocalAccept(t, "127.0.0.1:8080")
	ready, retry = invocationReady(context.Background(), url.URL{Host: "127.0.0.1:8080"})
	if !ready || !retry {
		t.Fatalf("python listener ready=%t retry=%t", ready, retry)
	}
}

func waitForLocalAccept(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 20*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener %s did not accept", addr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func Test400ReadFailureQuarantines(t *testing.T) {
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = buf.WriteString("HTTP/1.1 400 Bad Request\r\nContent-Length: 40\r\n\r\ncut")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	dispatch(t, &stubResolver{target: *target}, fs, nil)
	active, idle, _ := fs.ReplicaSets("fn")
	if active != 0 || idle != 0 {
		t.Fatalf("400 read failure active=%d idle=%d", active, idle)
	}
}

func TestComplete400IsReleased(t *testing.T) {
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "predecessor_unavailable", http.StatusBadRequest)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	rec := dispatch(t, &stubResolver{target: *target}, fs, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	active, idle, _ := fs.ReplicaSets("fn")
	if active != 0 || idle != 1 {
		t.Fatalf("complete 400 active=%d idle=%d", active, idle)
	}
}

func TestTimeoutStatusStaysUnresolved(t *testing.T) {
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream HTTP killed", http.StatusGatewayTimeout)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	dispatch(t, &stubResolver{target: *target}, fs, nil)
	active, idle, replicas := fs.ReplicaSets("fn")
	if active != 0 || idle != 0 || replicas != 1 || !fs.AtReplicaBudget("fn") {
		t.Fatalf("timeout status active=%d idle=%d replicas=%d budget=%t", active, idle, replicas, fs.AtReplicaBudget("fn"))
	}
}

func TestPreStartCancelReleasesWithoutDial(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	req := httptest.NewRequest(http.MethodPost, "/function/fn", bytes.NewReader([]byte(`{}`)))
	ctx, stop := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := dispatch(t, &stubResolver{target: *target, onCall: stop}, fs, req)
	if calls.Load() != 0 {
		t.Fatalf("pre-start cancel dialed %d times status %d", calls.Load(), rec.Code)
	}
	active, idle, _ := fs.ReplicaSets("fn")
	if active != 0 || idle != 1 {
		t.Fatalf("pre-start cancel active=%d idle=%d", active, idle)
	}
}

func TestResolverDelayConsumesTheOriginalBudget(t *testing.T) {
	prev := SetUpstreamDeadlineForTest(func(start time.Time) time.Time {
		return start.Add(150 * time.Millisecond)
	})
	defer SetUpstreamDeadlineForTest(prev)
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(withHealth(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		case <-time.After(2 * time.Second):
		}
	}))
	defer upstream.Close()
	defer close(release)
	target, _ := url.Parse(upstream.URL)
	host, _, _ := net.SplitHostPort(target.Host)
	fs := handlers.NewActiveReplicaStore("fn", "fn-a", host)
	started := time.Now()
	dispatch(t, &stubResolver{target: *target, delay: 100 * time.Millisecond}, fs, nil)
	if time.Since(started) > 220*time.Millisecond {
		t.Fatalf("budget was extended to %s", time.Since(started))
	}
	select {
	case <-entered:
	default:
		t.Fatal("the upstream call was not started")
	}
	active, idle, replicas := fs.ReplicaSets("fn")
	if idle != 0 || replicas != 1 {
		t.Fatalf("deadline expiry idle=%d active=%d replicas=%d", idle, active, replicas)
	}
}
