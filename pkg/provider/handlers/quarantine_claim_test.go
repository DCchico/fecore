package handlers

import (
	"context"
	"testing"
	"time"
)

func TestQuarantineExcludesDirectAndDependencyClaims(t *testing.T) {
	fs, err := InitFunctionStore(&memStorage{})
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["fn"] = &Function{
		name:             "fn",
		replicas:         1,
		deployedReplicas: 1,
		activeReplicas:   map[string]string{"fn-a": "10.0.0.8"},
		idleReplicas:     map[string]string{},
		idleReplicasTs:   map[string]time.Time{},
	}
	fs.RecordInvocationDetails("fn", "cam", "1", "10.0.0.8", false, true)
	if err := fs.QuarantineReplica("fn", "10.0.0.8", "req", "cam", "1"); err != nil {
		t.Fatal(err)
	}
	if err := fs.QuarantineReplica("fn", "10.0.0.8", "req", "cam", "1"); err == nil {
		t.Fatal("a second quarantine matched a replica that was already fenced")
	}
	fn := fs.deployedFunctions["fn"]
	if len(fn.activeReplicas) != 0 || len(fn.idleReplicas) != 0 || fn.replicas != 1 || !fs.AtReplicaBudget("fn") {
		t.Fatalf("quarantine changed capacity active=%d idle=%d replicas=%d budget=%t", len(fn.activeReplicas), len(fn.idleReplicas), fn.replicas, fs.AtReplicaBudget("fn"))
	}
	name, ip, ok, err := fs.GetIdleReplicaAtomic("fn", "next")
	if err != nil || ok || name != "" || ip != "" {
		t.Fatalf("direct reclaim got a quarantined replica %s %s ok=%t err=%v", name, ip, ok, err)
	}
	got, running := fs.GetInvocationDetails("fn", "cam", "1")
	if got != "Unknown" || running {
		t.Fatalf("dependency claim returned %s running=%t", got, running)
	}
	resolver := NewInvokeResolver(nil, nil, fs)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, _, _, err = resolver.Resolve("fn", "again", []DependencyInfo{{SenderVDAG: "cam", SenderSeq: "1"}}, "cam", "2", false, false, "", ctx)
	if err == nil {
		t.Fatal("resolver claimed a replacement for a quarantined replica")
	}
	if fn.replicas != 1 || len(fn.activeReplicas) != 0 || len(fn.idleReplicas) != 0 {
		t.Fatalf("resolver grew or reused the fenced replica replicas=%d active=%d idle=%d", fn.replicas, len(fn.activeReplicas), len(fn.idleReplicas))
	}
}
