package handlers

import (
	"sync"
	"testing"
)

func physical(id, pkg, core int, online bool, siblings []int) CPUTopology {
	return CPUTopology{ID: id, Online: online, Package: pkg, Core: core, Siblings: siblings}
}

// Twenty physical cores, SMT siblings at id+20, matching this c8220 before nosmt.
func twentyCoreSMT() []CPUTopology {
	var cpus []CPUTopology
	for core := 0; core < 20; core++ {
		pkg := 0
		if core >= 10 {
			pkg = 1
		}
		local := core
		if core >= 10 {
			local = core - 10
		}
		a := core
		b := core + 20
		sibs := []int{a, b}
		cpus = append(cpus,
			physical(a, pkg, local, true, sibs),
			physical(b, pkg, local, true, sibs),
		)
	}
	return cpus
}

func TestPoolMoreThanEightDistinctCores(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 12)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for i := 0; i < 10; i++ {
		cpu, err := pool.Reserve("track-" + string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		if seen[cpu] {
			t.Fatalf("cpu %d reused", cpu)
		}
		seen[cpu] = true
		if cpu >= 20 {
			t.Fatalf("selected SMT sibling %d instead of one thread per core", cpu)
		}
	}
}

func TestPoolDistinctFunctionsDoNotOverlap(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 12)
	if err != nil {
		t.Fatal(err)
	}
	a, err := pool.Reserve("alpha-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := pool.Reserve("beta-1")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("functions share cpu %d", a)
	}
}

func TestPoolRejectsOfflineAndSiblingDuplicate(t *testing.T) {
	cpus := []CPUTopology{
		physical(0, 0, 0, true, []int{0, 20}),
		physical(20, 0, 0, false, []int{0, 20}),
		physical(1, 0, 1, false, []int{1, 21}),
		physical(21, 0, 1, false, []int{1, 21}),
		physical(2, 0, 2, true, []int{2, 22}),
		physical(22, 0, 2, true, []int{2, 22}),
	}
	pool, err := BuildFunctionPool(cpus, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := pool.PoolCPUs()
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("pool = %v, want [0 2]", got)
	}
	for _, cpu := range got {
		if cpu == 20 || cpu == 22 || cpu == 1 || cpu == 21 {
			t.Fatalf("offline or sibling cpu %d entered the pool", cpu)
		}
	}
}

func TestPoolExhaustionDoesNotWrap(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Reserve("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Reserve("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Reserve("three"); err == nil {
		t.Fatal("expected exhaustion, got a wrapped cpu")
	}
}

func TestPoolReleaseAndReuse(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := pool.Reserve("one")
	if err != nil {
		t.Fatal(err)
	}
	pool.Release("one")
	again, err := pool.Reserve("two")
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatalf("released cpu %d was not reused, got %d", first, again)
	}
}

func TestPoolAdoptConflictFailsClosed(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Adopt("keep", 0); err != nil {
		t.Fatal(err)
	}
	if err := pool.Adopt("other", 0); err == nil {
		t.Fatal("expected conflict")
	}
	if closed, _ := pool.Closed(); !closed {
		t.Fatal("pool should be closed after a conflict")
	}
	if _, err := pool.Reserve("later"); err == nil {
		t.Fatal("closed pool accepted a reservation")
	}
}

func TestPoolAdoptOutsidePoolFailsClosed(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Adopt("stray", 7); err == nil {
		t.Fatal("expected outside-pool failure")
	}
	if closed, _ := pool.Closed(); !closed {
		t.Fatal("pool should be closed")
	}
}

func TestPoolReserveRace(t *testing.T) {
	pool, err := BuildFunctionPool(twentyCoreSMT(), 12)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	cpus := make([]int, 20)
	errs := make([]error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cpus[i], errs[i] = pool.Reserve("n" + string(rune(i)))
		}(i)
	}
	wg.Wait()
	ok := map[int]int{}
	failures := 0
	for i := 0; i < 20; i++ {
		if errs[i] != nil {
			failures++
			continue
		}
		ok[cpus[i]]++
	}
	if failures != 8 {
		t.Fatalf("failures=%d, want 8", failures)
	}
	if len(ok) != 12 {
		t.Fatalf("unique successes=%d", len(ok))
	}
	for cpu, n := range ok {
		if n != 1 {
			t.Fatalf("cpu %d assigned %d times", cpu, n)
		}
	}
}

func TestParseCPUList(t *testing.T) {
	got, err := parseCPUList("0-2,4,20-21")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0, 1, 2, 4, 20, 21}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
	if _, err := parseCPUList("5-1"); err == nil {
		t.Fatal("expected bad range")
	}
}
