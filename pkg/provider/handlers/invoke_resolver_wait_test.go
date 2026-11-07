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

func TestReservedIdleReusesTheDeployedBudget(t *testing.T) {
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
	if err != nil || !ok || name == "" || ip == "" {
		t.Fatalf("expected an existing replica, got %s %s %v %v", name, ip, ok, err)
	}
	if fs.deployedFunctions["timeline"].replicas != 4 {
		t.Fatalf("replicas grew to %d", fs.deployedFunctions["timeline"].replicas)
	}
	if len(fs.deployedFunctions["timeline"].idleReplicas) != 1 {
		t.Fatal("one reserved idle replica should remain")
	}
	if err := fs.UpdateReplicaStatusInactive("timeline", ip, "req"); err != nil {
		t.Fatal(err)
	}
	if len(fs.deployedFunctions["timeline"].activeReplicas) != 0 {
		t.Fatal("release left the replica active")
	}
	if len(fs.deployedFunctions["timeline"].idleReplicas) != 2 {
		t.Fatal("release did not return the replica to idle")
	}
	if !contains(fs.dependentContainers, ip) {
		t.Fatal("reclaim cleared the direct reservation")
	}
}
