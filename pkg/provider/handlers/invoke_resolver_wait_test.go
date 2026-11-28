package handlers

import (
	"context"
	"testing"
	"time"
)

func TestPollUntilStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := pollUntil(ctx, 5*time.Millisecond, func() bool { return false })
	if err == nil {
		t.Fatal("a wait with no predecessor must return when the deadline fires")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("wait ran for %s", time.Since(started))
	}
}

func TestCanceledDependencyWaitDoesNotClaim(t *testing.T) {
	fs, err := InitFunctionStore(&memStorage{})
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["timeline"] = &Function{
		name:             "timeline",
		replicas:         1,
		deployedReplicas: 1,
		activeReplicas:   map[string]string{},
		idleReplicas:     map[string]string{"timeline-a": "10.0.0.9"},
		idleReplicasTs:   map[string]time.Time{},
	}
	resolver := NewInvokeResolver(nil, nil, fs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = resolver.Resolve("timeline", "req", []DependencyInfo{{
		SenderVDAG: "user-1-home_timeline",
		SenderSeq:  "1",
	}}, "user-1-home_timeline", "2", false, false, "", ctx)
	if err == nil {
		t.Fatal("expected the canceled dependency wait to fail")
	}
	if len(fs.deployedFunctions["timeline"].activeReplicas) != 0 {
		t.Fatal("canceled wait claimed a replica")
	}
	if len(fs.deployedFunctions["timeline"].idleReplicas) != 1 {
		t.Fatal("idle replica was removed")
	}
}

func TestNoIdleReplicaAtBudgetExitsOnCancel(t *testing.T) {
	fs, err := InitFunctionStore(&memStorage{})
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["timeline"] = &Function{
		name:             "timeline",
		replicas:         4,
		deployedReplicas: 4,
		activeReplicas:   map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"},
		idleReplicas:     map[string]string{},
		idleReplicasTs:   map[string]time.Time{},
	}
	resolver := NewInvokeResolver(nil, nil, fs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = resolver.Resolve("timeline", "req", nil, "vdag", "1", false, false, "", ctx)
	if err == nil {
		t.Fatal("expected no eligible replica to fail on cancel")
	}
	if fs.deployedFunctions["timeline"].replicas != 4 {
		t.Fatalf("replicas changed to %d", fs.deployedFunctions["timeline"].replicas)
	}
}

func TestInactiveReleaseAfterDeleteDoesNotPanic(t *testing.T) {
	fs := &FunctionStore{}
	err := fs.UpdateReplicaStatusInactive("gone", "10.0.0.1", "req")
	if err == nil {
		t.Fatal("missing function must return an error, not panic")
	}
}

func TestReservedIdleStaysWithItsPredecessor(t *testing.T) {
	fs, err := InitFunctionStore(&memStorage{})
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["timeline"] = &Function{
		name:             "timeline",
		replicas:         4,
		deployedReplicas: 4,
		activeReplicas:   map[string]string{},
		idleReplicas:     map[string]string{"a": "10.0.0.1", "b": "10.0.0.2"},
		idleReplicasTs:   map[string]time.Time{},
	}
	fs.dependentContainers = []string{"10.0.0.1", "10.0.0.2"}
	name, ip, ok, err := fs.GetIdleReplicaAtomic("timeline", "req")
	if err != nil || ok || name != "" || ip != "" {
		t.Fatalf("another caller took a reserved replica %s %s ok=%t err=%v", name, ip, ok, err)
	}
	if fs.deployedFunctions["timeline"].replicas != 4 {
		t.Fatalf("replicas grew to %d", fs.deployedFunctions["timeline"].replicas)
	}
	if len(fs.deployedFunctions["timeline"].idleReplicas) != 2 {
		t.Fatal("both reserved replicas should still be idle")
	}
	if !contains(fs.dependentContainers, "10.0.0.1") || !contains(fs.dependentContainers, "10.0.0.2") {
		t.Fatal("the direct reservations were cleared")
	}
}

func TestDirectHandoffSkipsAnInterveningCaller(t *testing.T) {
	fs, err := InitFunctionStore(&memStorage{})
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["fn"] = &Function{
		name:             "fn",
		replicas:         2,
		deployedReplicas: 2,
		activeReplicas:   map[string]string{},
		idleReplicas:     map[string]string{"hold": "10.0.0.1", "free": "10.0.0.2"},
		idleReplicasTs:   map[string]time.Time{},
	}
	fs.RecordInvocationDetails("fn", "camA", "1", "10.0.0.1", false, true)
	name, ip, ok, err := fs.GetIdleReplicaAtomic("fn", "other-ns")
	if err != nil || !ok || ip != "10.0.0.2" || name != "free" {
		t.Fatalf("intervening caller got %s %s ok=%t err=%v", name, ip, ok, err)
	}
	resolver := NewInvokeResolver(nil, nil, fs)
	got, _, _, err := resolver.Resolve("fn", "consumer", []DependencyInfo{{SenderVDAG: "camA", SenderSeq: "1"}}, "camA", "2", false, true, "", context.Background())
	if err != nil || got.Hostname() != "10.0.0.1" {
		t.Fatalf("consumer resolved %s err=%v", got.String(), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, _, err = resolver.Resolve("fn", "later", []DependencyInfo{{SenderVDAG: "camA", SenderSeq: "2"}}, "camA", "3", false, false, "", ctx)
	if err == nil {
		t.Fatal("a later successor claimed a replica while its predecessor was still running")
	}
	if _, stillActive := fs.deployedFunctions["fn"].activeReplicas["hold"]; !stillActive {
		t.Fatal("the predecessor container was released to the later successor")
	}
	if _, stolen := fs.deployedFunctions["fn"].activeReplicas["free"]; !stolen || len(fs.deployedFunctions["fn"].activeReplicas) != 2 {
		t.Fatal("the later successor took the intervening caller's replica")
	}
}

func TestUnclaimablePredecessorDoesNotRunOnAnotherReplica(t *testing.T) {
	fs, err := InitFunctionStore(&memStorage{})
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["fn"] = &Function{
		name:             "fn",
		replicas:         1,
		deployedReplicas: 1,
		activeReplicas:   map[string]string{},
		idleReplicas:     map[string]string{"live": "10.0.0.2"},
		idleReplicasTs:   map[string]time.Time{},
	}
	fs.RecordInvocationDetails("fn", "vDAG1", "14", "10.62.0.74", false, true)
	resolver := NewInvokeResolver(nil, nil, fs)
	_, _, _, err = resolver.Resolve("fn", "frame15", []DependencyInfo{{SenderVDAG: "vDAG1", SenderSeq: "14"}}, "vDAG1", "15", false, true, "", context.Background())
	if err == nil {
		t.Fatal("frame 15 ran after its recorded predecessor container was gone")
	}
	if len(fs.deployedFunctions["fn"].idleReplicas) != 1 {
		t.Fatal("the other replica was claimed for the broken handoff")
	}
}
