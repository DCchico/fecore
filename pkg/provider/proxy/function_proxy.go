/*
Based on "github.com/openfaas/faas-provider/proxy"
*/
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/types"

	"github.com/google/uuid"

	// "github.com/openfaas/faas-provider/proxy"

	"github.gatech.edu/faasedge/fecore/pkg/provider/handlers"
	"github.gatech.edu/faasedge/fecore/pkg/timec"
)

const (
	watchdogPort        = "8080"
	defaultContentType  = "text/plain"
	dialTimeout         = 30 * time.Second  // Increased from 10s to 30s
	responseTimeout     = 120 * time.Second // upstream function budget, outside the 90s watchdog
	maxIdleConns        = 100
	maxIdleConnsPerHost = 20
	idleConnTimeout     = 30 * time.Second
	maxRetries          = 20   // Increased from 5 to 20
	baseBackoff         = 100  // ms - Increased from 50ms to 100ms
	maxBackoff          = 2000 // ms - Increased from 500ms to 2000ms
	// proxyOverallDeadline caps the whole retry loop. The orchestrator
	// client timeout is 150s. This cap is the provider's own 120s budget.
	proxyOverallDeadline = 120 * time.Second
)

// upstreamDeadline is the absolute end of this request's provider budget.
// It is taken when the request is accepted, so resolver waiting consumes it.
// A client abort does not move it, and a late start does not grant another
// responseTimeout. Tests replace the function; the production budget stays
// responseTimeout.
var upstreamDeadline = func(start time.Time) time.Time {
	return start.Add(responseTimeout)
}

// SetUpstreamDeadlineForTest replaces the budget clock. The returned function
// restores the previous one.
func SetUpstreamDeadlineForTest(fn func(time.Time) time.Time) func(time.Time) time.Time {
	prev := upstreamDeadline
	if fn != nil {
		upstreamDeadline = fn
	}
	return prev
}

// functionResolver is the claim path the proxy calls. The production resolver
// is *handlers.InvokeResolver. Tests supply a transport seam.
type functionResolver interface {
	Resolve(functionName string, requestID string, dependencies []handlers.DependencyInfo, vDAGID string, sequenceID string, releaseLocalState bool, reserveLocalState bool, dataCentricDependency string, ctx context.Context) (url.URL, bool, bool, error)
}

func waitOrAbandoned(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// invocationReady waits until the function process accepts connections.
// of-watchdog 0.9.10 listens on 8080 and writes its lock file before the
// upstream on 5000 is up, and its GET /_/health follows that lock. A TCP
// dial to 5000 delivers no HTTP request. Any other port is the test double
// or a direct upstream, so the caller submits once.
func invocationReady(ctx context.Context, functionAddr url.URL) (bool, bool) {
	if ctx.Err() != nil {
		return false, false
	}
	host, port, err := net.SplitHostPort(functionAddr.Host)
	if err != nil || port != watchdogPort {
		return true, true
	}
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, "5000"))
	if err == nil {
		_ = conn.Close()
		return true, true
	}
	if ctx.Err() != nil {
		return false, false
	}
	// The four independent applications are the HTTP server on 8080.
	// of-watchdog also listens there, but it does not identify itself as
	// the Python runtime, and its upstream is still port 5000.
	if directPythonListener(ctx, host) {
		return true, true
	}
	return false, true
}

func directPythonListener(ctx context.Context, host string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+net.JoinHostPort(host, watchdogPort)+"/_/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64))
	return resp.StatusCode == http.StatusOK && strings.Contains(resp.Header.Get("Server"), "Python")
}

func stopProxyRetries(w http.ResponseWriter, requestID, functionName string, attempts, retries int, started time.Time, reason error) {
	retryMs := time.Since(started).Milliseconds()
	log.Printf("[function_proxy] stop retries requestID=%s function=%s attempts=%d retries=%d retry_ms=%d reason=%v",
		requestID, functionName, attempts, retries, retryMs, reason)
	w.Header().Set("X-Proxy-Attempts", strconv.Itoa(attempts))
	w.Header().Set("X-Proxy-Retries", strconv.Itoa(retries))
	w.Header().Set("X-Proxy-Retry-Ms", strconv.FormatInt(retryMs, 10))
	httputil.Errorf(w, http.StatusGatewayTimeout, "proxy retries stopped after %d attempts (%d ms): %v", attempts, retryMs, reason)
}

func setProxyBudgetHeaders(h http.Header, attempts, retries int, started time.Time) {
	h.Set("X-Proxy-Attempts", strconv.Itoa(attempts))
	h.Set("X-Proxy-Retries", strconv.Itoa(retries))
	h.Set("X-Proxy-Retry-Ms", strconv.FormatInt(time.Since(started).Milliseconds(), 10))
}

// DispatchForTest runs the production proxy loop. The resolver and client are
// the transport seams; the disposal decisions are not reimplemented here.
func DispatchForTest(w http.ResponseWriter, r *http.Request, client *http.Client, resolver functionResolver, fs *handlers.FunctionStore) {
	proxyRequest(w, r, client, resolver, fs)
}

func NewHandlerFunc(config types.FaaSConfig, resolver *handlers.InvokeResolver, fs *handlers.FunctionStore) http.HandlerFunc {
	if resolver == nil {
		panic("NewHandlerFunc: empty proxy handler resolver, cannot be nil")
	}

	// Create a custom transport with tuned parameters
	customTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			start := time.Now()
			conn, err := (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext(ctx, network, addr)
			if err != nil {
				log.Printf("[function_proxy] Connection failed to %s after %v: %v",
					addr, time.Since(start), err)
			}
			return conn, err
		},
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   2 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Create custom client with the transport
	customClient := &http.Client{
		Transport: customTransport,
		Timeout:   responseTimeout,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}

		switch r.Method {
		case http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodGet,
			http.MethodOptions,
			http.MethodHead:
			proxyRequest(w, r, customClient, resolver, fs)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// proxyRequest handles the actual resolution of and then request to the function service.
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver functionResolver, fs *handlers.FunctionStore) {
	/* Begin function replica setup */
	fnSetupStart := time.Now()
	// The detached execution deadline is this instant plus the provider
	// budget. Resolver waiting is inside it.
	providerDeadline := upstreamDeadline(fnSetupStart)
	ctx := originalReq.Context()
	// The 120s budget covers resolver waits and the later proxy retries.
	ctx, cancelOverall := context.WithTimeout(ctx, proxyOverallDeadline)
	defer cancelOverall()
	// State management: get userID
	vDAGID := originalReq.Header.Get("vDAGID")
	workflowID := originalReq.Header.Get("workflowID")
	sequenceID := originalReq.Header.Get("sequence")
	isForwardDownstream := ""

	pathVars := mux.Vars(originalReq)
	functionName := pathVars["name"]
	requestID := functionName + "-" + uuid.New().String()
	firstRequestID := originalReq.Header.Get("firstRequestID")
	if firstRequestID == "" {
		firstRequestID = requestID
	}
	defer timec.RecordDuration("(function_proxy.go) proxyRequest() <requestID="+requestID+">", time.Now())
	if functionName == "" {
		httputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
		return
	}

	// Check for state-location and state-dependencies headers
	stateLocation := originalReq.Header.Get("state-location")
	stateDependencies := originalReq.Header.Get("state-dependencies")
	dependentStateLocation := originalReq.Header.Get("dependent-state-location")

	var releaseLocalState bool = false
	var reserveLocalState bool = false

	var dependencies []handlers.DependencyInfo
	if dependentStateLocation == "direct" && stateDependencies != "" {
		log.Printf("[function_proxy] Direct state access mode detected for function %s", functionName)
		log.Printf("[function_proxy] State dependencies: %s", stateDependencies)

		// Parse state-dependencies JSON
		err := json.Unmarshal([]byte(stateDependencies), &dependencies)
		if err != nil {
			log.Printf("[function_proxy] Error parsing state-dependencies: %v", err)
		} else {
			for _, dep := range dependencies {
				log.Printf("[function_proxy] Dependency - SenderVDAG: %s, SenderSeq: %s", dep.SenderVDAG, dep.SenderSeq)
			}
		}
		// Set releaseLocalState based on whether we're transitioning
		releaseLocalState = (stateLocation != "direct")
	} else if stateLocation == "direct" && dependentStateLocation != "direct" {
		// Shared Memory → Direct transition: reserve the current container for future dependencies
		log.Printf("[function_proxy] Shared Memory → Direct transition detected for function %s", functionName)
		reserveLocalState = true
	}

	dataCentricDependency := originalReq.Header.Get("data-centric-dependency")

	// Finish the body before any wait. The server notices a client close only
	// after this request's body has been consumed; otherwise a canceled caller
	// leaves the resolver blocked until the 60s deadline.
	var bodyBytes []byte
	if originalReq.Body != nil {
		bodyBytes, _ = io.ReadAll(originalReq.Body)
		originalReq.Body.Close()
	}

	// Accept is before replica resolution, so provider queue time stays inside
	// the interval the function measures to its first timestamp.
	acceptAt := time.Now()
	functionAddr, coldStart, isInitialized, resolveErr := resolver.Resolve(functionName, requestID, dependencies, vDAGID, sequenceID, releaseLocalState, reserveLocalState, dataCentricDependency, ctx)
	var replicaReleased bool
	var quarantined bool
	// phase: 0 not dispatched, 1 upstream call in flight or running, 2 finished.
	var phase atomic.Int32
	var phaseMu sync.Mutex
	var abortOnce sync.Once
	endFlight := func() {
		phaseMu.Lock()
		phase.Store(2)
		phaseMu.Unlock()
	}
	noteAbort := func(outcome string) {
		abortOnce.Do(func() {
			ip := ""
			if functionAddr.Host != "" {
				ip = strings.Split(functionAddr.Host, ":")[0]
			}
			fs.NoteClientAbort(functionName, ip, requestID, vDAGID, sequenceID, outcome)
		})
	}
	go func() {
		<-ctx.Done()
		phaseMu.Lock()
		defer phaseMu.Unlock()
		if phase.Load() == 1 {
			noteAbort("continuing")
		}
	}()
	// The upstream call keeps running after the caller disconnects, but only
	// until the deadline taken at accept. A dropped socket is not the end,
	// and the wait for a replica does not start a new budget.
	execCtx, cancelExec := context.WithDeadline(context.Background(), providerDeadline)
	defer cancelExec()
	defer func() {
		if resolveErr != nil || replicaReleased || quarantined || functionAddr.Host == "" {
			return
		}
		ip := strings.Split(functionAddr.Host, ":")[0]
		if relErr := fs.ReleaseReplica(functionName, ip, requestID, vDAGID, sequenceID); relErr != nil {
			log.Printf("[function_proxy] release on exit requestID=%s function=%s ip=%s err=%v", requestID, functionName, ip, relErr)
		}
	}()
	if resolveErr != nil {
		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, resolveErr.Error())
		httputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
		return
	}
	fs.NotifyExecutionAdmission(
		originalReq.Header.Get("X-Pending-Workflow"),
		originalReq.Header.Get("X-Pending-Namespace"),
		originalReq.Header.Get("X-Pending-Id"),
		originalReq.Header.Get("X-Pending-Generation"),
		originalReq.Header.Get("X-Pending-Epoch"),
	)

	maxRetries := 1000
	connectionRetries := 0
	attempts := 0
	var totalConnectTime int64

	proxyStart := time.Now()
	connStart := time.Now()

	ip := strings.Split(functionAddr.Host, ":")[0]

	fnSetupTime := time.Since(fnSetupStart).Milliseconds()
	log.Printf("[function_proxy] vDAGID = %s, workflowID = %s, fnSetupTime = %d", vDAGID, workflowID, fnSetupTime)
	/* End function replica setup */

	var response *http.Response
	var proxyReq *http.Request
	var err error
	fnExecStart := time.Now()
	totalConnectTime = -1
	backoff := baseBackoff
	var wroteNs atomic.Int64
	var firstNs atomic.Int64
	var completedErrorBody []byte
	if !time.Now().Before(providerDeadline) {
		httputil.Errorf(w, http.StatusGatewayTimeout, "provider budget exhausted before execution")
		return
	}

	for connectionRetries < maxRetries {
		if phase.Load() == 0 && ctx.Err() != nil {
			noteAbort("pre_start")
			stopProxyRetries(w, requestID, functionName, attempts, connectionRetries, proxyStart, ctx.Err())
			return
		}
		ready, retryHealth := invocationReady(ctx, functionAddr)
		if !ready {
			if !retryHealth {
				noteAbort("pre_start")
				stopProxyRetries(w, requestID, functionName, attempts, connectionRetries, proxyStart, ctx.Err())
				return
			}
			phase.Store(0)
			connectionRetries++
			log.Printf("[function_proxy] function port is not accepting requestID=%s retry=%d", requestID, connectionRetries)
			jitter := rand.Intn(backoff / 4)
			if !waitOrAbandoned(ctx, time.Duration(backoff+jitter)*time.Millisecond) {
				noteAbort("pre_start")
				stopProxyRetries(w, requestID, functionName, attempts, connectionRetries, proxyStart, ctx.Err())
				return
			}
			backoff = int(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}
		connStart = time.Now()
		proxyReq, err = buildProxyRequest(originalReq, functionAddr, pathVars["params"], bodyBytes)
		if err != nil {
			httputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
			return
		}
		proxyReq.Header.Set("X-Provider-Accept-Realtime-Ns", strconv.FormatInt(acceptAt.UnixNano(), 10))

		// Same absolute deadline for every attempt. This does not add
		// responseTimeout again after the resolver wait.
		reqCtx, cancel := context.WithCancel(execCtx)
		reqCtx = httptrace.WithClientTrace(reqCtx, &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) {
				wroteNs.Store(time.Now().UnixNano())
			},
			GotFirstResponseByte: func() {
				firstNs.Store(time.Now().UnixNano())
			},
		})
		phase.Store(1)
		attempts++
		response, err = proxyClient.Do(proxyReq.WithContext(reqCtx))
		if err != nil {
			cancel()
			if dialFailed(err) {
				phase.Store(0)
				connectionRetries++
				log.Printf("[function_proxy] Retry %d for %s: %v (cold start: %t)", connectionRetries, functionName, err, coldStart)
				if ctx.Err() != nil {
					noteAbort("pre_start")
					stopProxyRetries(w, requestID, functionName, attempts, connectionRetries, proxyStart, ctx.Err())
					return
				}
			} else {
				// The handler may still be running. Do not retry into this
				// container and do not return it to the idle pool.
				quarantined = true
				replicaReleased = true
				endFlight()
				qerr := fs.QuarantineReplica(functionName, ip, requestID, vDAGID, sequenceID)
				log.Printf("[function_proxy] execution end unknown requestID=%s err=%v quarantine=%v", requestID, err, qerr)
				stopProxyRetries(w, requestID, functionName, attempts, connectionRetries, proxyStart, err)
				return
			}

			// Calculate backoff with jitter
			jitter := rand.Intn(backoff / 4)
			sleepTime := backoff + jitter
			if !waitOrAbandoned(ctx, time.Duration(sleepTime)*time.Millisecond) {
				stopProxyRetries(w, requestID, functionName, attempts, connectionRetries, proxyStart, ctx.Err())
				return
			}

			// Increase backoff for next retry (capped)
			backoff = int(math.Min(float64(backoff*2), float64(maxBackoff)))
		} else if response.StatusCode == http.StatusInternalServerError || response.StatusCode == http.StatusBadRequest {
			// A status line is not an execution end. Read the whole body
			// before deciding. A short or failed read quarantines; there is
			// no second attempt on this container. reqCtx stays live until
			// the original deadline so a slow but complete body can finish.
			body, readErr := readUpstreamBody(reqCtx, response.Body)
			cancel()
			if readErr != nil {
				quarantined = true
				replicaReleased = true
				endFlight()
				qerr := fs.QuarantineReplica(functionName, ip, requestID, vDAGID, sequenceID)
				log.Printf("[function_proxy] error body incomplete status=%d requestID=%s err=%v quarantine=%v", response.StatusCode, requestID, readErr, qerr)
				httputil.Errorf(w, http.StatusBadGateway, "function status %d body did not complete", response.StatusCode)
				return
			}
			// A finished response is not retried. An empty 500 can be the
			// application after it has already run; body length is not a
			// pre-execution signal. Cold start waits on GET /_/health.
			log.Printf("[function_proxy] completed application error status=%d requestID=%s bytes=%d", response.StatusCode, requestID, len(body))
			completedErrorBody = body
			response.Body = io.NopCloser(bytes.NewReader(body))
			break
		} else if response.StatusCode != http.StatusOK {
			// Gateway and watchdog timeouts can write a body while the
			// function is still running. That is not a confirmed stop.
			cancel()
			if response.Body != nil {
				response.Body.Close()
			}
			quarantined = true
			replicaReleased = true
			endFlight()
			qerr := fs.QuarantineReplica(functionName, ip, requestID, vDAGID, sequenceID)
			log.Printf("[function_proxy] unconfirmed status=%d requestID=%s quarantine=%v", response.StatusCode, requestID, qerr)
			httputil.Errorf(w, http.StatusBadGateway, "function status %d did not confirm execution end", response.StatusCode)
			return
		} else {
			defer cancel()
			break
		}
	}
	totalConnectTime = connStart.Sub(proxyStart).Milliseconds()
	fnExecTime := time.Since(fnExecStart).Milliseconds()

	if connectionRetries == maxRetries {
		log.Printf("[retryProxyRequest] Unable to contact function %s <requestID=%s> at %s: connection time exceeded\n", functionName, requestID, ip)
		httputil.Errorf(w, http.StatusInternalServerError, "Connection time exceeded")
		return
	}
	if err != nil {
		log.Printf("[retryProxyRequest] Unable to contact function %s at %s: %s\n", functionName, ip, err)
		httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
		return
	}

	// A fully read 400 or 500 is the function's own completed error.
	// Release only after that read. Any other status was quarantined above.
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusInternalServerError {
		log.Printf("[function_proxy] Forwarding completed application error %d from function %s", response.StatusCode, functionName)
		ip = strings.Split(functionAddr.Host, ":")[0]
		if proxyReq != nil && proxyReq.URL != nil {
			phaseMu.Lock()
			phase.Store(2)
			phaseMu.Unlock()
			replicaReleased = true
			err = fs.ReleaseReplica(functionName, ip, requestID, vDAGID, sequenceID)
			if err != nil {
				log.Printf("[function_proxy] Unable to update function replica status '%s': %s\n", proxyReq.URL.String(), err.Error())
				httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
				return
			}
		}
		clientHeader := w.Header()
		copyHeaders(clientHeader, &response.Header)
		w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))
		w.Header().Set("Request-ID", requestID)
		setProxyBudgetHeaders(w.Header(), attempts, connectionRetries, proxyStart)
		w.WriteHeader(response.StatusCode)
		if len(completedErrorBody) > 0 {
			_, _ = w.Write(completedErrorBody)
		}
		return
	}

	if response.StatusCode != http.StatusOK {
		quarantined = true
		replicaReleased = true
		endFlight()
		qerr := fs.QuarantineReplica(functionName, ip, requestID, vDAGID, sequenceID)
		log.Printf("[function_proxy] unconfirmed status=%d requestID=%s quarantine=%v", response.StatusCode, requestID, qerr)
		httputil.Errorf(w, http.StatusBadGateway, "function status %d did not confirm execution end", response.StatusCode)
		return
	}

	log.Printf("[function_proxy] Success connecting to function <requestID=%s, execTime=%d>", requestID, fnExecTime)

	proxyElapsed := time.Since(proxyStart)
	timec.RecordDuration("(function_proxy.go) proxyRequest() : [ProxyRequestTime] <requestID="+requestID+", coldStart="+strconv.FormatBool(coldStart)+", retries="+strconv.Itoa(connectionRetries)+", connectTime="+strconv.Itoa(int(totalConnectTime))+" ms>", proxyStart)

	var functionBody []byte
	if response.Body != nil {
		functionBody, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			quarantined = true
			replicaReleased = true
			endFlight()
			qerr := fs.QuarantineReplica(functionName, ip, requestID, vDAGID, sequenceID)
			log.Printf("[function_proxy] execution end unknown while reading body requestID=%s err=%v quarantine=%v", requestID, err, qerr)
			return
		}
		response.Body = io.NopCloser(bytes.NewReader(functionBody))
	}
	phaseMu.Lock()
	phase.Store(2)
	phaseMu.Unlock()

	log.Printf("[function_proxy] Invocation of '%s' (coldStart: %t) took %d ms with %d retries \n", functionName, coldStart, proxyElapsed.Milliseconds(), connectionRetries)
	fs.RecordInvocationTime(proxyElapsed, coldStart)
	log.Printf("[function_proxy] CALLING UpdateFunctionStats(%s, %d, %d)", functionName, fnSetupTime, fnExecTime)
	fs.UpdateFunctionStats(functionName, fnSetupTime, fnExecTime)

	ip = strings.Split(functionAddr.Host, ":")[0]
	if proxyReq != nil && proxyReq.URL != nil {
		replicaReleased = true
		err = fs.ReleaseReplica(functionName, ip, requestID, vDAGID, sequenceID)
		if err != nil {
			log.Printf("[function_proxy] Unable to update function replica status '%s': %s\n", proxyReq.URL.String(), err.Error())
			httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
			return
		}
	} else {
		log.Printf("[function_proxy] proxyReq or proxyReq.URL was nil, unable to update replica status.")
	}
	// Forward the response to the downstream function
	if workflowID != "" && vDAGID != "" && isForwardDownstream != "" {
		w.WriteHeader(response.StatusCode)
		contentType := getContentType(originalReq.Header, response.Header)
		if err := sendDownstreamRequest(functionName, workflowID, vDAGID, contentType, firstRequestID, response); err != nil {
			log.Printf("[function_proxy] Error in sendDownstreamRequest: %v", err)
		}
	} else {
		clientHeader := w.Header()
		copyHeaders(clientHeader, &response.Header)
		w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))
		w.Header().Set("Request-ID", requestID)
		w.Header().Set("isInitialized", strconv.FormatBool(isInitialized))
		w.Header().Set("X-Provider-Accept-Realtime-Ns", strconv.FormatInt(acceptAt.UnixNano(), 10))
		if wrote := wroteNs.Load(); wrote > 0 {
			w.Header().Set("X-Provider-Wrote-Realtime-Ns", strconv.FormatInt(wrote, 10))
		}
		if first := firstNs.Load(); first > 0 {
			w.Header().Set("X-Provider-First-Byte-Realtime-Ns", strconv.FormatInt(first, 10))
		}
		log.Printf("CLOCK accept_realtime_ns=%d wrote_realtime_ns=%d first_byte_realtime_ns=%d request_id=%s", acceptAt.UnixNano(), wroteNs.Load(), firstNs.Load(), requestID)
		setProxyBudgetHeaders(w.Header(), attempts, connectionRetries, proxyStart)
		log.Printf("[function_proxy] budget requestID=%s attempts=%d retries=%d retry_ms=%d", requestID, attempts, connectionRetries, time.Since(proxyStart).Milliseconds())

		w.WriteHeader(response.StatusCode)
		if len(functionBody) > 0 {
			_, _ = w.Write(functionBody)
		}
	}
}

func readUpstreamBody(ctx context.Context, body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	defer body.Close()
	done := make(chan struct{})
	var buf []byte
	var err error
	go func() {
		buf, err = io.ReadAll(body)
		close(done)
	}()
	select {
	case <-ctx.Done():
		_ = body.Close()
		return nil, ctx.Err()
	case <-done:
		return buf, err
	}
}

func dialFailed(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	return false
}

// buildProxyRequest creates a request object for the proxy request, it will ensure that
// the original request headers are preserved as well as setting openfaas system headers
func buildProxyRequest(originalReq *http.Request, baseURL url.URL, extraPath string, body []byte) (*http.Request, error) {

	host := baseURL.Host
	if baseURL.Port() == "" {
		host = baseURL.Host + ":" + watchdogPort
	}

	url := url.URL{
		Scheme:   baseURL.Scheme,
		Host:     host,
		Path:     extraPath,
		RawQuery: originalReq.URL.RawQuery,
	}

	upstreamReq, err := http.NewRequest(originalReq.Method, url.String(), bytes.NewReader(body))
	if err != nil {
		log.Printf("[ERROR] Failed to create new request: %v", err)
		return nil, err
	}

	copyHeaders(upstreamReq.Header, &originalReq.Header)

	if len(originalReq.Host) > 0 && upstreamReq.Header.Get("X-Forwarded-Host") == "" {
		upstreamReq.Header["X-Forwarded-Host"] = []string{originalReq.Host}
	}
	if upstreamReq.Header.Get("X-Forwarded-For") == "" {
		upstreamReq.Header["X-Forwarded-For"] = []string{originalReq.RemoteAddr}
	}

	return upstreamReq, nil
}

// copyHeaders clones the header values from the source into the destination.
func copyHeaders(destination http.Header, source *http.Header) {
	for k, v := range *source {
		vClone := make([]string, len(v))
		copy(vClone, v)
		destination[k] = vClone
	}
}

// getContentType resolves the correct Content-Type for a proxied function.
func getContentType(request http.Header, proxyResponse http.Header) (headerContentType string) {
	responseHeader := proxyResponse.Get("Content-Type")
	requestHeader := request.Get("Content-Type")

	if len(responseHeader) > 0 {
		headerContentType = responseHeader
	} else if len(requestHeader) > 0 {
		headerContentType = requestHeader
	} else {
		headerContentType = defaultContentType
	}

	return headerContentType
}
