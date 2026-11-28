package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"
	fecore "github.gatech.edu/faasedge/fecore/pkg"
	"github.gatech.edu/faasedge/fecore/pkg/provider/storage"
	"github.gatech.edu/faasedge/fecore/pkg/timec"
)

/*
 * All read functions return copies and not references
 * All write functions should update the storage
 */

/* In-memory map for keeping track of functions that have been created */
type FunctionStore struct {
	deployedFunctions map[string]*Function
	functionStats     map[string]*FunctionStats
	storageManager    storage.StorageManager

	coldStartTimes      []int64
	invocationTimesCold []int64 // cold and warm distinguish how many retries
	invocationTimesWarm []int64
	fecoreIP            string
	fecorePort          int
	stateflowIP         string
	stateflowPort       string
	nextIP              net.IP
	//ipconfigs map[string]string
	ipconfigs sync.Map

	/* Begin netns test */
	nsMu      sync.RWMutex
	nextNS    int64
	netnsList []int64
	nextNSIP  net.IP
	/* End netns test */

	statsChan chan FunctionStat

	/* Begin mutexes */
	mu       sync.RWMutex // Added a rw mutex and things like reading the whole map require a global map anyways, TODO check if there are other strategies
	metricMu sync.RWMutex
	ipamMu   sync.RWMutex
	dfMu     sync.RWMutex
	statsMu  sync.RWMutex
	/* End mutexes */

	// Map to store invocation details
	invocationDetails map[string]map[string]map[string]string // functionName -> vDAGID -> sequenceID -> containerIP

	// Map to store invocation details by container IP
	invocationDetailsByIP map[string]InvocationInfo

	dependentContainers []string // Dependent containers by IP
}

// Define a struct to hold invocation information
type InvocationInfo struct {
	FunctionName string
	VDAGID       string
	SequenceID   string
}

type FunctionStats struct {
	startupTimes [100]int64
	execTimes    [100]int64

	startupTimesPos int
	execTimesPos    int
}

type FunctionStat struct {
	fn          string
	startupTime int64
	execTime    int64
}

func InitFunctionStore(storageManager storage.StorageManager) (*FunctionStore, error) {
	fs := FunctionStore{
		/* Init mutexes */
		mu:       sync.RWMutex{},
		metricMu: sync.RWMutex{},
		ipamMu:   sync.RWMutex{},
		nsMu:     sync.RWMutex{},
		dfMu:     sync.RWMutex{},
		//ipconfigs:         make(map[string]string),
		deployedFunctions:     make(map[string]*Function),
		functionStats:         make(map[string]*FunctionStats),
		storageManager:        storageManager,
		ipconfigs:             sync.Map{},
		statsChan:             make(chan FunctionStat, 100),
		invocationDetails:     make(map[string]map[string]map[string]string),
		invocationDetailsByIP: make(map[string]InvocationInfo),
	}

	sFns, err := storageManager.GetAllFunctions()
	if err != nil {
		return nil, err
	}

	// log.Println(sFns)
	for _, sFn := range sFns {
		fn := getFunction(&sFn)
		cns, err := storageManager.GetContainersForFunction(fn.name)
		// log.Println(cns)
		if err != nil {
			return nil, err
		}
		for _, c := range cns {
			fn.idleReplicas[c.Name] = c.Ip
			fn.idleReplicasTs[c.Name] = time.Now()
		}
		// State management
		// fn.UserToFunctionReplica = make(map[string]map[string]fecore.StickinessRecord)
		fs.deployedFunctions[fn.name] = &fn
	}
	// log.Println(fs.deployedFunctions)

	fs.nextIP = net.IPv4(10, 62, 0, 1)

	/* Begin netns test */
	fs.nextNS = 10
	fs.nextNSIP = net.IPv4(10, 62, 100, 10)
	/* End netns test */

	// Get Fecore outbound IP
	fs.fecorePort = 8081
	fs.fecoreIP, err = getLocalIP()
	if err != nil {
		log.Println("Error getting IP address:", err)
	} else {
		log.Println("Local IP address:", fs.fecoreIP)
	}
	return &fs, nil
}

// Get outbound IP address of current server
func getLocalIP() (string, error) {
	// The host default route is CloudLab's control network. FECORE_BIND_ADDR
	// is the experiment address this process already listens on, and it is
	// the address advertised to the orchestrator.
	if bind := os.Getenv("FECORE_BIND_ADDR"); bind != "" {
		ip := net.ParseIP(bind)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() {
			return "", fmt.Errorf("FECORE_BIND_ADDR %q is not a specific IPv4 address", bind)
		}
		return ip.String(), nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no suitable IP address found")
}

/**
 * Cleanup function replicas that are not being used for 300 secs
 */
func (fs *FunctionStore) CleanupDaemon(client *containerd.Client, cni gocni.CNI) {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	log.Println("[CleanupDaemon] RUNNING")

	// TODO using default, is there a way to get all namespaces? do we need it?
	ctx := namespaces.WithNamespace(context.Background(), fecore.DefaultFunctionNamespace)

	currTs := time.Now()
	for name, fn := range fs.deployedFunctions {
		for cn, ts := range fn.idleReplicasTs {
			if currTs.Sub(ts).Seconds() >= 600 { // TODO 300 seconds harcoded
				log.Println("[CleanupDaemon] Cleaning up replica :", cn)
				delete(fs.deployedFunctions[name].idleReplicas, cn)
				delete(fs.deployedFunctions[name].idleReplicasTs, cn)
				deleteReplica(ctx, client, *fs.deployedFunctions[name], cni, cn)
				fs.deployedFunctions[name].replicas -= 1
				sendReplicaUpdateAsync(fs.fecoreIP, fs.fecorePort, fs.stateflowIP, fs.stateflowPort, name, -1)
				// State management
				// Also delete the association in UserToFunctionReplica map.
				// Iterate over all users and remove this replica.
				// for userID, replicas := range fn.UserToFunctionReplica {
				// 	if _, exists := replicas[cn]; exists {
				// 		delete(replicas, cn)
				// 		if len(replicas) == 0 {
				// 			// Optionally clean up if no more replicas are associated with the user.
				// 			delete(fn.UserToFunctionReplica, userID)
				// 		}
				// 	}
				// }
			}
		}
	}
	// TODO: let stateflow knows a full map update async if there are replicas being reclaimed
}

/* Gets a function's metadata from DB, creates/populates Function struct
 * and returns pointer to that struct.
 */
func (fs *FunctionStore) GetDeployedFunctions() ([]Function, error) {
	fs.dfMu.RLock()
	defer fs.dfMu.RUnlock()
	fns := make([]Function, 0)
	for _, value := range fs.deployedFunctions {
		fns = append(fns, *value)
	}
	return fns, nil
}

/* Gets a function's metadata from DB, creates/populates Function struct
 * and returns pointer to that struct
 * TODO check namespace
 */
func (fs *FunctionStore) GetDeployedFunction(name string, requestID string) (fn Function, err error) {
	defer timec.RecordDuration("(function_store.go) GetDeployedFunction() <requestID="+requestID+">", time.Now())
	fs.dfMu.RLock()
	defer fs.dfMu.RUnlock()
	if fn, ok := fs.deployedFunctions[name]; ok {
		return *fn, nil
	}
	return Function{}, fmt.Errorf("[GetDeployedFunction] Unable to get function from `store %s", name)
}

/* Add deployed function to the store */
func (fs *FunctionStore) AddDeployedFunction(fn *Function) error {
	err := fs.storageManager.InsertFunction(getStorageFunction(fn))
	if err != nil {
		return err
	}
	for _, c := range getContainers(fn) {
		err = fs.storageManager.InsertContainer(c)
		if err != nil {
			return err
		}
	}
	fs.dfMu.Lock()
	fs.deployedFunctions[fn.name] = fn
	fs.dfMu.Unlock()

	/* Begin init stats for this fn */
	fs.statsMu.Lock()
	defer fs.statsMu.Unlock()
	fnStats := FunctionStats{}
	fnStats.startupTimesPos = 0
	fnStats.execTimesPos = 0
	fs.functionStats[fn.name] = &fnStats
	/* End init stats for this fn */

	log.Printf("[addDeployedFunction] Added deployed function '%s' in namespace '%s'", fn.name, fn.namespace)
	return nil
}

/* Updates a function's metadata in DB */
func (fs *FunctionStore) UpdateDeployedFunction(fn *Function) error {
	log.Printf("[updateDeployedFunction] Updated metadata for deployed function '%s'", fn.name)
	return nil
}

/* Remove function from store and db */
func (fs *FunctionStore) RemoveDeployedFunction(name string) error {
	fs.dfMu.RLock()
	fn := fs.deployedFunctions[name]

	err := fs.storageManager.DeleteFunction(name)
	if err != nil {
		fs.dfMu.RUnlock()
		return err
	}
	// A name can be absent from the memory map after a provider restart or a
	// previous delete. DeleteFunction still succeeds when no row matches, so
	// the container walk must tolerate a nil entry. Leaving this read lock
	// held panics the request and blocks every later delete and deploy.
	if fn != nil {
		for _, c := range getContainers(fn) {
			fs.storageManager.DeleteContainer(c.Name)
		}
	}
	fs.dfMu.RUnlock()

	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	if _, ok := fs.deployedFunctions[name]; ok {
		delete(fs.deployedFunctions, name)
		fs.dropInvocationDetails(name)
		log.Printf("[removeDeployedFunction] Removed deployed function '%s'", name)
		return nil
	}
	return fmt.Errorf("[removeDeployedFunction] Unable to locate function %s", name)
}

// dropInvocationDetails forgets direct-container records for a removed
// function. A later deploy must not treat a dead address as the current
// predecessor. Caller holds dfMu.
func (fs *FunctionStore) dropInvocationDetails(name string) {
	seqs := fs.invocationDetails[name]
	for _, bySeq := range seqs {
		for _, ip := range bySeq {
			if info, ok := fs.invocationDetailsByIP[ip]; ok && info.FunctionName == name {
				delete(fs.invocationDetailsByIP, ip)
			}
			if contains(fs.dependentContainers, ip) {
				fs.dependentContainers = remove(fs.dependentContainers, ip)
			}
		}
	}
	delete(fs.invocationDetails, name)
}

/* Gets the container name of an idle (warm) Function replica and sets the
 * container replica to Active */
func (fs *FunctionStore) GetIdleReplica(fn string, requestID string) (string, string, error) {
	defer timec.RecordDuration("(function_store.go) GetIdleReplica() <requestID="+requestID+">", time.Now())
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	var name string
	var ip string
	for k, v := range fs.deployedFunctions[fn].idleReplicas {
		name = k
		ip = v
		break
	}
	delete(fs.deployedFunctions[fn].idleReplicas, name)
	delete(fs.deployedFunctions[fn].idleReplicasTs, name)

	fs.deployedFunctions[fn].activeReplicas[name] = ip

	return name, ip, nil
}

/* Adds new active replica */
func (fs *FunctionStore) AddActiveReplica(fn string, cn string, ip string, requestID string) error {
	defer timec.RecordDuration("(function_store.go) AddActiveReplica() <requestID="+requestID+">", time.Now())
	// err := fs.storageManager.InsertContainer(storage.Container{Name: cn, ParentFunction: fn, Ip: ip})
	// if err != nil {
	// 	return err
	// }
	fs.dfMu.Lock()
	fs.deployedFunctions[fn].activeReplicas[cn] = ip
	fs.deployedFunctions[fn].replicas += 1
	sendReplicaUpdateAsync(fs.fecoreIP, fs.fecorePort, fs.stateflowIP, fs.stateflowPort, fn, fs.deployedFunctions[fn].replicas)
	fs.dfMu.Unlock()

	return nil
}

/* Change container from active to inactive
 * Note: lookup by ip and container name is not avaiable in resolver
 */
func (fs *FunctionStore) UpdateReplicaStatusInactive(fn string, ip string, requestID string) error {
	return fs.ReleaseReplica(fn, ip, requestID, "", "")
}

// ReleaseReplica returns a container to idle only after its invocation ended.
func (fs *FunctionStore) ReleaseReplica(fn, ip, requestID, vdag, seq string) error {
	defer timec.RecordDuration("(function_store.go) UpdateReplicaStatusInactive() Returning container w/ IP "+ip+" to inactive pool <requestID="+requestID+">", time.Now())
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	fnState := fs.deployedFunctions[fn]
	if fnState == nil || fnState.activeReplicas == nil {
		LogOccupancy("release", requestID, fn, vdag, seq, "", ip, "ambiguous")
		return fmt.Errorf("[UpdateReplicaStatusInactive] function %s is not deployed", fn)
	}
	cn := ""
	for c, cIp := range fnState.activeReplicas {
		if cIp == ip {
			cn = c
			break
		}
	}
	if cn == "" {
		LogOccupancy("release", requestID, fn, vdag, seq, "", ip, "ambiguous")
		return fmt.Errorf("[UpdateReplicaStatusInactive] Unable to get function %s container %s in active set", fn, cn)
	}
	delete(fnState.activeReplicas, cn)
	if fnState.idleReplicas == nil {
		fnState.idleReplicas = map[string]string{}
	}
	if fnState.idleReplicasTs == nil {
		fnState.idleReplicasTs = map[string]time.Time{}
	}
	fnState.idleReplicas[cn] = ip
	fnState.idleReplicasTs[cn] = time.Now()
	LogOccupancy("release", requestID, fn, vdag, seq, cn, ip, "released")
	return nil
}

// NoteClientAbort records that the caller disconnected. It does not release
// the replica. outcome=pre_start means the function HTTP had not started.
// outcome=continuing means execution is still active.
func (fs *FunctionStore) NoteClientAbort(fn, ip, requestID, vdag, seq, outcome string) {
	if fs == nil {
		return
	}
	LogOccupancy("client_abort", requestID, fn, vdag, seq, fs.ContainerByIP(ip), ip, outcome)
}

// NewActiveReplicaStore is the in-memory store used by the proxy regression.
// It does not open a database or a container.
func NewActiveReplicaStore(fn, container, ip string) *FunctionStore {
	return &FunctionStore{
		deployedFunctions: map[string]*Function{
			fn: {
				name:             fn,
				replicas:         1,
				deployedReplicas: 1,
				activeReplicas:   map[string]string{container: ip},
				idleReplicas:     map[string]string{},
				idleReplicasTs:   map[string]time.Time{},
			},
		},
		invocationDetails:     map[string]map[string]map[string]string{},
		invocationDetailsByIP: map[string]InvocationInfo{},
		statsChan:             make(chan FunctionStat, 8),
	}
}

// ReplicaSets reports where one function's replicas sit.
func (fs *FunctionStore) ReplicaSets(fn string) (active, idle, replicas int) {
	if fs == nil {
		return 0, 0, 0
	}
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	function := fs.deployedFunctions[fn]
	if function == nil {
		return 0, 0, 0
	}
	return len(function.activeReplicas), len(function.idleReplicas), function.replicas
}

// QuarantineReplica removes a container from the active set without returning
// it to idle. Used when the invocation's end was not observed. The deployed
// replica budget does not grow to replace it.
func (fs *FunctionStore) QuarantineReplica(fn, ip, requestID, vdag, seq string) error {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	fnState := fs.deployedFunctions[fn]
	if fnState == nil || fnState.activeReplicas == nil {
		LogOccupancy("release", requestID, fn, vdag, seq, "", ip, "unresolved")
		return fmt.Errorf("[QuarantineReplica] function %s is not deployed", fn)
	}
	cn := ""
	for c, cIp := range fnState.activeReplicas {
		if cIp == ip {
			cn = c
			break
		}
	}
	if cn == "" {
		LogOccupancy("release", requestID, fn, vdag, seq, "", ip, "unresolved")
		return fmt.Errorf("[QuarantineReplica] container for %s is not active", ip)
	}
	delete(fnState.activeReplicas, cn)
	LogOccupancy("release", requestID, fn, vdag, seq, cn, ip, "unresolved")
	log.Printf("[QuarantineReplica] container %s request %s left active and was not returned to idle", cn, requestID)
	return nil
}

func (fs *FunctionStore) RecordColdStartTime(csTime time.Duration, requestID string) {
	defer timec.RecordDuration("(function_store.go) RecordColdStartTime() <requestID="+requestID+">", time.Now())
	fs.metricMu.Lock()
	defer fs.metricMu.Unlock()

	fs.coldStartTimes = append(fs.coldStartTimes, csTime.Milliseconds())
}

func (fs *FunctionStore) GetColdStartTimes() []int64 {
	fs.metricMu.RLock()
	defer fs.metricMu.RUnlock()

	return fs.coldStartTimes
}

func (fs *FunctionStore) RecordInvocationTime(time time.Duration, coldStart bool) {
	fs.metricMu.Lock()
	defer fs.metricMu.Unlock()

	if coldStart {
		fs.invocationTimesCold = append(fs.invocationTimesCold, time.Milliseconds())
	} else {
		fs.invocationTimesWarm = append(fs.invocationTimesWarm, time.Milliseconds())
	}
}

func (fs *FunctionStore) GetInvocationTime(cold bool) []int64 {
	fs.metricMu.RLock()
	defer fs.metricMu.RUnlock()

	if cold {
		return fs.invocationTimesCold
	} else {
		return fs.invocationTimesWarm
	}
}

func (fs *FunctionStore) GetNetNS() (int64, string) {
	fs.nsMu.Lock()
	defer fs.nsMu.Unlock()
	thisIP := fs.nextNSIP.To4().String()
	i := fs.nextNSIP.To4()
	v := uint(i[0])<<24 + uint(i[1])<<16 + uint(i[2])<<8 + uint(i[3])
	v += 1
	v3 := byte(v & 0xFF)
	v2 := byte((v >> 8) & 0xFF)
	v1 := byte((v >> 16) & 0xFF)
	v0 := byte((v >> 24) & 0xFF)
	fs.nextNSIP = net.IPv4(v0, v1, v2, v3)
	thisNS := fs.nextNS
	fs.nextNS += 1
	return thisNS, thisIP
}

func (fs *FunctionStore) UpdateFunctionStats(fn string, startupTime int64, execTime int64) {
	log.Printf("[UpdateFunctionStats()] Got entry: fn=%s, startupTime=%d, execTime=%d", fn, startupTime, execTime)
	fs.statsChan <- FunctionStat{fn, startupTime, execTime}
}

func (fs *FunctionStore) ProcessFunctionStats() {
	/* Use circular buffer for stats
	 * Based on idea from:
	 * https://stackoverflow.com/questions/55598220/efficiently-keeping-a-collection-of-the-last-n-pushed-items */
	MAX_ENTRIES := 100
	for {
		select {
		case stat := <-fs.statsChan:
			fn := stat.fn
			fs.statsMu.Lock()
			if _, ok := fs.functionStats[fn]; ok {
				startupTimesPos := fs.functionStats[fn].startupTimesPos
				execTimesPos := fs.functionStats[fn].execTimesPos
				fs.functionStats[fn].startupTimes[startupTimesPos] = stat.startupTime
				fs.functionStats[fn].execTimes[execTimesPos] = stat.execTime
				fs.functionStats[fn].startupTimesPos = (startupTimesPos + 1) % MAX_ENTRIES
				fs.functionStats[fn].execTimesPos = (execTimesPos + 1) % MAX_ENTRIES
				log.Printf("[ProcessFunctionStats()] Added entry #%d: fn=%s, startupTime=%d, execTime=%d", startupTimesPos, fn, fs.functionStats[fn].startupTimes[startupTimesPos], fs.functionStats[fn].execTimes[execTimesPos])
			}
			fs.statsMu.Unlock()
		}
	}
}

/* Utility functions */
func getFunction(f *storage.Function) Function {
	labels := map[string]string{}
	json.Unmarshal([]byte(f.Labels), &labels)

	annotations := map[string]string{}
	json.Unmarshal([]byte(f.Annotations), &annotations)

	envVars := map[string]string{}
	json.Unmarshal([]byte(f.EnvVars), &annotations)

	secrets := []string{}
	json.Unmarshal([]byte(f.Secrets), &annotations)

	return Function{
		name:           f.Name,
		namespace:      f.Namespace,
		image:          f.Image,
		labels:         labels,
		idleReplicas:   make(map[string]string),
		idleReplicasTs: make(map[string]time.Time),
		activeReplicas: make(map[string]string),
		annotations:    annotations,
		secrets:        secrets,
		secretsPath:    f.SecretsPath,
		envVars:        envVars,
		envProcess:     f.EnvProcess,
		memoryLimit:    f.MemoryLimit,
	}
}

func getStorageFunction(f *Function) storage.Function {
	lables, _ := json.Marshal(f.labels)
	annotations, _ := json.Marshal(f.annotations)
	envVars, _ := json.Marshal(f.envVars)
	secrets, _ := json.Marshal(f.secrets)

	return storage.Function{
		Name:        f.name,
		Namespace:   f.namespace,
		Image:       f.image,
		Labels:      string(lables),
		Annotations: string(annotations),
		Secrets:     string(secrets),
		SecretsPath: f.secretsPath,
		EnvVars:     string(envVars),
		EnvProcess:  f.envProcess,
		MemoryLimit: f.memoryLimit,
	}
}

func getContainers(f *Function) []storage.Container {
	var cns []storage.Container
	for cname, ip := range f.activeReplicas {
		cns = append(cns, storage.Container{Name: cname, ParentFunction: f.name, Ip: ip})
	}
	for cname, ip := range f.idleReplicas {
		cns = append(cns, storage.Container{Name: cname, ParentFunction: f.name, Ip: ip})
	}
	return cns
}

// State management: mark the bitmap given function, userID and replicaID
// func (fs *FunctionStore) MarkUserToReplica(functionName string, userID string, replicaName string, stickiness fecore.StickinessRecord) error {
// 	fs.dfMu.Lock()
// 	defer fs.dfMu.Unlock()

// 	// Check if the function exists
// 	fn, exists := fs.deployedFunctions[functionName]
// 	if !exists {
// 		return fmt.Errorf("[MarkUserToReplica] function %s not found", functionName)
// 	}

// 	// Check if the replica exists
// 	_, replicaExists := fn.idleReplicas[replicaName]
// 	if !replicaExists {
// 		_, replicaExists = fn.activeReplicas[replicaName]
// 		if !replicaExists {
// 			return fmt.Errorf("[MarkUserToReplica] container replica %s not found for function %s", replicaName, functionName)
// 		}
// 	}

// 	// // Ensure the nested map for the user exists
// 	// if fn.UserToFunctionReplica[userID] == nil {
// 	// 	fn.UserToFunctionReplica[userID] = make(map[string]fecore.StickinessRecord)
// 	// }

// 	// // Mark or update the stickiness record
// 	// fn.UserToFunctionReplica[userID][replicaName] = stickiness // Example usage, assuming you have defined levels of stickiness

// 	return nil
// }

// AtReplicaBudget reports that this function already has its deployed replica
// count. Zero deployedReplicas preserves the historical cold-start path.
func (fs *FunctionStore) AtReplicaBudget(fn string) bool {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	function, exists := fs.deployedFunctions[fn]
	if !exists || function.deployedReplicas <= 0 {
		return false
	}
	return function.replicas >= function.deployedReplicas
}

// GetIdleReplicaAtomic attempts to get an idle replica atomically, returning empty strings if none available
func (fs *FunctionStore) GetIdleReplicaAtomic(fn string, requestID string) (string, string, bool, error) {
	defer timec.RecordDuration("(function_store.go) GetIdleReplicaAtomic() <requestID="+requestID+">", time.Now())
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	function, exists := fs.deployedFunctions[fn]
	if !exists {
		return "", "", false, fmt.Errorf("function %s not found", fn)
	}

	// Check if there are any idle replicas
	if len(function.idleReplicas) == 0 {
		return "", "", false, nil
	}

	// Get first available replica
	var name string
	var ip string
	for k, v := range function.idleReplicas {
		ip = v
		if contains(fs.dependentContainers, ip) {
			continue
		} else {
			name = k
			break
		}
	}

	// A direct-reserved replica keeps the predecessor object for the
	// dependent call. Another namespace waits for a replica that is not
	// reserved. Reclaiming it would overwrite that object, and creating
	// another replica would pass the deployed budget.
	if name == "" {
		return "", "", false, nil
	}

	// Move replica from idle to active
	delete(function.idleReplicas, name)
	delete(function.idleReplicasTs, name)
	function.activeReplicas[name] = ip

	return name, ip, true, nil
}

// RecordInvocationDetails records the association between functionName, vDAGID, sequenceID, and containerIP
func (fs *FunctionStore) RecordInvocationDetails(functionName, vDAGID, sequenceID, containerIP string, releaseLocalState bool, reserveLocalState bool) {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	if _, exists := fs.invocationDetails[functionName]; !exists {
		fs.invocationDetails[functionName] = make(map[string]map[string]string)
	}
	if _, exists := fs.invocationDetails[functionName][vDAGID]; !exists {
		fs.invocationDetails[functionName][vDAGID] = make(map[string]string)
	}

	fs.invocationDetails[functionName][vDAGID][sequenceID] = containerIP
	fs.invocationDetailsByIP[containerIP] = InvocationInfo{
		FunctionName: functionName,
		VDAGID:       vDAGID,
		SequenceID:   sequenceID,
	}
	if releaseLocalState {
		if contains(fs.dependentContainers, containerIP) {
			fs.dependentContainers = remove(fs.dependentContainers, containerIP)
		}
	}
	if reserveLocalState {
		if !contains(fs.dependentContainers, containerIP) {
			fs.dependentContainers = append(fs.dependentContainers, containerIP)
		}
	}
}

// ReleaseDirectReservation makes a replica eligible again after the call
// published its object outside the process. A direct hold that is no longer
// the only copy must not occupy the deployed budget.
func (fs *FunctionStore) ReleaseDirectReservation(containerIP string) {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	if contains(fs.dependentContainers, containerIP) {
		fs.dependentContainers = remove(fs.dependentContainers, containerIP)
	}
}

// GetInvocationDetails retrieves the container IP and status using functionName, vDAGID, and sequenceID
func (fs *FunctionStore) GetInvocationDetails(functionName, vDAGID, sequenceID string) (string, bool) {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	if vdagMap, exists := fs.invocationDetails[functionName]; exists {
		if seqMap, exists := vdagMap[vDAGID]; exists {
			if containerIP, exists := seqMap[sequenceID]; exists {
				// Check if the container is still active
				for _, ip := range fs.deployedFunctions[functionName].activeReplicas {
					if ip == containerIP {
						return containerIP, true
					}
				}
				for name, ip := range fs.deployedFunctions[functionName].idleReplicas {
					if ip == containerIP {
						delete(fs.invocationDetails[functionName][vDAGID], sequenceID)
						delete(fs.deployedFunctions[functionName].idleReplicas, name)
						delete(fs.deployedFunctions[functionName].idleReplicasTs, name)
						fs.deployedFunctions[functionName].activeReplicas[name] = ip
						if !contains(fs.dependentContainers, ip) {
							fs.dependentContainers = append(fs.dependentContainers, ip)
						}
						return ip, false
					}
				}
				// TODO: Unknown case: containerIP is not in idleReplicas or activeReplicas
				log.Printf("[GetInvocationDetails] recorded container %s is not claimable function=%s vdag=%s seq=%s", containerIP, functionName, vDAGID, sequenceID)
				return "Unknown", false
			}
		}
	}
	return "", false
}

// ContainerByIP returns the containerd id currently mapped to ip.
// An empty result means the caller must not invent a cgroup id.
func (fs *FunctionStore) ContainerByIP(ip string) string {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	for _, fn := range fs.deployedFunctions {
		if fn == nil {
			continue
		}
		for name, cip := range fn.activeReplicas {
			if cip == ip {
				return name
			}
		}
		for name, cip := range fn.idleReplicas {
			if cip == ip {
				return name
			}
		}
	}
	return ""
}

// NotifyExecutionAdmission tells the orchestrator that this request now holds
// a replica. The send is asynchronous and does not measure the function body.
func (fs *FunctionStore) NotifyExecutionAdmission(workflow, namespace, id, generation, epoch string) {
	if fs == nil {
		return
	}
	go sendExecutionAdmission(fs.stateflowIP, fs.stateflowPort, workflow, namespace, id, generation, epoch)
}

// contains checks if a slice contains a specific element.
func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}

// remove removes a specific element from a slice.
func remove(slice []string, item string) []string {
	for i, v := range slice {
		if v == item {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func (fs *FunctionStore) IsReplicaInitialized(fn string, replicaIP string) bool {
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	if contains(fs.deployedFunctions[fn].coldReplicas, replicaIP) {
		fs.deployedFunctions[fn].coldReplicas = remove(fs.deployedFunctions[fn].coldReplicas, replicaIP)
		return false
	}
	return true
}
