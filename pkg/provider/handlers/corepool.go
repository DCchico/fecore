package handlers

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FunctionPoolSize is the common dedicated function pool on each worker.
// The remaining physical cores stay available for the provider and the OS.
const FunctionPoolSize = 12

// CPUTopology is one logical CPU as reported by sysfs.
type CPUTopology struct {
	ID      int
	Online  bool
	Package int
	Core    int
	// Siblings lists every logical CPU in this physical core, online or not.
	Siblings []int
}

// CorePool assigns one online hardware thread of a distinct physical core.
// A reservation is global across function names. The pool does not wrap.
type CorePool struct {
	mu       sync.Mutex
	pool     []int
	used     map[int]string
	byName   map[string]int
	closed   bool
	reason   string
	numaNote string
}

// BuildFunctionPool selects the first size physical cores in package/core order.
// Each selected core contributes its lowest online thread. Sibling threads are
// not a second assignment. Fewer than size online physical cores is an error.
func BuildFunctionPool(cpus []CPUTopology, size int) (*CorePool, error) {
	if size <= 0 {
		return nil, fmt.Errorf("function pool size must be positive")
	}
	type group struct {
		pkg, core int
		online    []int
		siblings  []int
	}
	groups := map[[2]int]*group{}
	var order [][2]int
	for _, cpu := range cpus {
		key := [2]int{cpu.Package, cpu.Core}
		g, ok := groups[key]
		if !ok {
			g = &group{pkg: cpu.Package, core: cpu.Core}
			groups[key] = g
			order = append(order, key)
		}
		if len(cpu.Siblings) > 0 {
			g.siblings = append(g.siblings, cpu.Siblings...)
		} else {
			g.siblings = append(g.siblings, cpu.ID)
		}
		if cpu.Online {
			g.online = append(g.online, cpu.ID)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i][0] != order[j][0] {
			return order[i][0] < order[j][0]
		}
		return order[i][1] < order[j][1]
	})
	var reps []int
	for _, key := range order {
		g := groups[key]
		if len(g.online) == 0 {
			continue
		}
		sort.Ints(g.online)
		rep := g.online[0]
		for _, id := range g.online {
			if id < rep {
				rep = id
			}
		}
		// A sibling that is also online is the same physical core.
		reps = append(reps, rep)
	}
	if len(reps) < size {
		return nil, fmt.Errorf("need %d online physical cores, found %d", size, len(reps))
	}
	pool := &CorePool{
		pool:   append([]int(nil), reps[:size]...),
		used:   map[int]string{},
		byName: map[string]int{},
	}
	return pool, nil
}

// ClosedPool rejects every reservation. Dedicated placement fails closed.
func ClosedPool(reason string) *CorePool {
	return &CorePool{closed: true, reason: reason, used: map[int]string{}, byName: map[string]int{}}
}

func (p *CorePool) PoolCPUs() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.pool...)
}

func (p *CorePool) Closed() (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed, p.reason
}

func (p *CorePool) failClosedLocked(reason string) {
	p.closed = true
	p.reason = reason
}

// Reserve gives name a distinct pool CPU. It does not reuse a busy CPU and
// does not wrap. Reserving the same name twice returns the existing CPU.
func (p *CorePool) Reserve(name string) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("container name is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, fmt.Errorf("function core pool closed: %s", p.reason)
	}
	if cpu, ok := p.byName[name]; ok {
		return cpu, nil
	}
	for _, cpu := range p.pool {
		if _, taken := p.used[cpu]; !taken {
			p.used[cpu] = name
			p.byName[name] = cpu
			return cpu, nil
		}
	}
	return 0, fmt.Errorf("function core pool exhausted (%d cores)", len(p.pool))
}

// Release returns the CPU reserved for name. A missing name is not an error.
func (p *CorePool) Release(name string) {
	if p == nil || name == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cpu, ok := p.byName[name]
	if !ok {
		return
	}
	delete(p.byName, name)
	if p.used[cpu] == name {
		delete(p.used, cpu)
	}
}

// Adopt records a core already held by a running container. A conflict or a
// CPU outside the pool closes the pool.
func (p *CorePool) Adopt(name string, cpu int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !containsInt(p.pool, cpu) {
		p.failClosedLocked(fmt.Sprintf("container %s holds cpu %d outside the function pool", name, cpu))
		return fmt.Errorf("%s", p.reason)
	}
	if owner, ok := p.used[cpu]; ok && owner != name {
		p.failClosedLocked(fmt.Sprintf("cpu %d held by both %s and %s", cpu, owner, name))
		return fmt.Errorf("%s", p.reason)
	}
	if existing, ok := p.byName[name]; ok && existing != cpu {
		p.failClosedLocked(fmt.Sprintf("container %s recorded on cpu %d and cpu %d", name, existing, cpu))
		return fmt.Errorf("%s", p.reason)
	}
	p.used[cpu] = name
	p.byName[name] = cpu
	return nil
}

func containsInt(list []int, needle int) bool {
	for _, item := range list {
		if item == needle {
			return true
		}
	}
	return false
}

var (
	dedicatedMu   sync.Mutex
	dedicatedPool *CorePool
)

// SetDedicatedPool installs the process-wide pool used by create and delete.
func SetDedicatedPool(pool *CorePool) {
	dedicatedMu.Lock()
	dedicatedPool = pool
	dedicatedMu.Unlock()
}

func currentDedicatedPool() *CorePool {
	dedicatedMu.Lock()
	defer dedicatedMu.Unlock()
	return dedicatedPool
}

// ReserveDedicatedCore reserves one pool CPU for a new container.
func ReserveDedicatedCore(name string) (int, error) {
	pool := currentDedicatedPool()
	if pool == nil {
		return 0, fmt.Errorf("dedicated core pool is not initialized")
	}
	return pool.Reserve(name)
}

// ReleaseDedicatedCore returns a container's core after the container is gone.
func ReleaseDedicatedCore(name string) {
	currentDedicatedPool().Release(name)
}

// DiscoverFunctionPool reads sysfs and selects FunctionPoolSize physical cores.
func DiscoverFunctionPool() (*CorePool, string, error) {
	online, err := readCPUList("/sys/devices/system/cpu/online")
	if err != nil {
		return nil, "", err
	}
	onlineSet := map[int]bool{}
	for _, id := range online {
		onlineSet[id] = true
	}
	entries, err := os.ReadDir("/sys/devices/system/cpu")
	if err != nil {
		return nil, "", err
	}
	var cpus []CPUTopology
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "cpu") {
			continue
		}
		id, err := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
		if err != nil {
			continue
		}
		topo := filepath.Join("/sys/devices/system/cpu", name, "topology")
		pkg, err1 := readIntFile(filepath.Join(topo, "physical_package_id"))
		core, err2 := readIntFile(filepath.Join(topo, "core_id"))
		sibsRaw, err3 := os.ReadFile(filepath.Join(topo, "thread_siblings_list"))
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		sibs, err := parseCPUList(strings.TrimSpace(string(sibsRaw)))
		if err != nil {
			return nil, "", fmt.Errorf("%s siblings: %w", name, err)
		}
		cpus = append(cpus, CPUTopology{
			ID: id, Online: onlineSet[id], Package: pkg, Core: core, Siblings: sibs,
		})
	}
	pool, err := BuildFunctionPool(cpus, FunctionPoolSize)
	if err != nil {
		return nil, "", err
	}
	numa := readNUMAMap()
	pool.numaNote = numa
	note := fmt.Sprintf("function_pool=%v remaining_physical_cores_not_pinned numa=%s", pool.PoolCPUs(), numa)
	return pool, note, nil
}

// AdoptRunningCgroups reconstructs reservations from leaf cpusets.
// An inconsistent or out-of-pool cpuset closes the pool.
func AdoptRunningCgroups(pool *CorePool) error {
	root := "/sys/fs/cgroup/faasedge-fn"
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), "cpuset.cpus"))
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}
		cpus, err := parseCPUList(text)
		if err != nil {
			pool.mu.Lock()
			pool.failClosedLocked(fmt.Sprintf("%s cpuset %q: %v", entry.Name(), text, err))
			reason := pool.reason
			pool.mu.Unlock()
			return fmt.Errorf("%s", reason)
		}
		if len(cpus) != 1 {
			pool.mu.Lock()
			pool.failClosedLocked(fmt.Sprintf("%s cpuset %q is not one physical core", entry.Name(), text))
			reason := pool.reason
			pool.mu.Unlock()
			return fmt.Errorf("%s", reason)
		}
		if err := pool.Adopt(entry.Name(), cpus[0]); err != nil {
			return err
		}
		log.Printf("[corepool] adopted %s on cpu %d", entry.Name(), cpus[0])
	}
	return nil
}

func readNUMAMap() string {
	entries, err := os.ReadDir("/sys/devices/system/node")
	if err != nil {
		return "unavailable"
	}
	var parts []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "node") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/sys/devices/system/node", entry.Name(), "cpulist"))
		if err != nil {
			continue
		}
		parts = append(parts, entry.Name()+"="+strings.TrimSpace(string(raw)))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "unavailable"
	}
	return strings.Join(parts, " ")
}

func readCPUList(path string) ([]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCPUList(strings.TrimSpace(string(raw)))
}

func readIntFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}

func parseCPUList(text string) ([]int, error) {
	if text == "" {
		return nil, fmt.Errorf("empty cpu list")
	}
	var out []int
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			ends := strings.SplitN(part, "-", 2)
			start, err1 := strconv.Atoi(ends[0])
			end, err2 := strconv.Atoi(ends[1])
			if err1 != nil || err2 != nil || end < start {
				return nil, fmt.Errorf("bad cpu range %q", part)
			}
			for id := start; id <= end; id++ {
				out = append(out, id)
			}
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad cpu %q", part)
		}
		out = append(out, id)
	}
	return out, nil
}
