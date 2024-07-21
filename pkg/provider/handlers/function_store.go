package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
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

	nextIP net.IP
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
	/* End mutexes */
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
		deployedFunctions: make(map[string]*Function),
		functionStats:     make(map[string]*FunctionStats),
		storageManager:    storageManager,
		ipconfigs:         sync.Map{},
		statsChan:         make(chan FunctionStat, 100),
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

	return &fs, nil
}

/**
 * Cleanup function replicas that are not being used for 100 secs
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
			if currTs.Sub(ts).Seconds() >= 100 { // TODO 100 seconds harcoded
				log.Println("[CleanupDaemon] Cleaning up replica :", cn)
				delete(fs.deployedFunctions[name].idleReplicas, cn)
				delete(fs.deployedFunctions[name].idleReplicasTs, cn)
				deleteReplica(ctx, client, *fs.deployedFunctions[name], cni, cn)
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
	fs.dfMu.RUnlock()

	err := fs.storageManager.DeleteFunction(name)
	if err != nil {
		return err
	}
	for _, c := range getContainers(fn) {
		fs.storageManager.DeleteContainer(c.Name)
	}

	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()
	if _, ok := fs.deployedFunctions[name]; ok {
		delete(fs.deployedFunctions, name)
		log.Printf("[removeDeployedFunction] Removed deployed function '%s'", name)
		return nil
	}
	return fmt.Errorf("[removeDeployedFunction] Unable to locate function %s", name)
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
	fs.dfMu.Unlock()

	return nil
}

/* Change container from active to inactive
 * Note: lookup by ip and container name is not avaiable in resolver
 */
func (fs *FunctionStore) UpdateReplicaStatusInactive(fn string, ip string, requestID string) error {
	defer timec.RecordDuration("(function_store.go) UpdateReplicaStatusInactive() Returning container w/ IP "+ip+" to inactive pool <requestID="+requestID+">", time.Now())
	fs.dfMu.Lock()
	defer fs.dfMu.Unlock()

	cn := ""
	for c, cIp := range fs.deployedFunctions[fn].activeReplicas {
		if cIp == ip {
			cn = c
			break
		}
	}
	if cn == "" {
		return fmt.Errorf("[UpdateReplicaStatusInactive] Unable to get function %s container %s in active set", fn, cn)
	}
	delete(fs.deployedFunctions[fn].activeReplicas, cn)
	fs.deployedFunctions[fn].idleReplicas[cn] = ip
	fs.deployedFunctions[fn].idleReplicasTs[cn] = time.Now()
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
			startupTimesPos := fs.functionStats[fn].startupTimesPos
			execTimesPos := fs.functionStats[fn].execTimesPos
			fs.functionStats[fn].startupTimes[startupTimesPos] = stat.startupTime
			fs.functionStats[fn].execTimes[execTimesPos] = stat.execTime
			fs.functionStats[fn].startupTimesPos = (startupTimesPos + 1) % MAX_ENTRIES
			fs.functionStats[fn].execTimesPos = (execTimesPos + 1) % MAX_ENTRIES
			log.Printf("[ProcessFunctionStats()] Added entry #%d: fn=%s, startupTime=%d, execTime=%d", startupTimesPos, fn, fs.functionStats[fn].startupTimes[startupTimesPos], fs.functionStats[fn].execTimes[execTimesPos])
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
