package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"
	"github.gatech.edu/faasedge/fecore/pkg/provider/storage"
	fecore "github.gatech.edu/faasedge/fecore/pkg"
)

/*
 * All read functions return copies and not references
 * All write functions should update the storage
 */

/* In-memory map for keeping track of functions that have been created */
type FunctionStore struct {
	mu					sync.RWMutex // Added a rw mutex and things like reading the whole map require a global map anyways, TODO check if there are other strategies
	deployedFunctions	map[string]*Function
	storageManager		storage.StorageManager
	
	metricMu			sync.RWMutex
	coldStartTime       []int64 // TODO move to function
}

func InitFunctionStore(storageManager storage.StorageManager) (*FunctionStore, error) {
	fs := FunctionStore {
		mu: sync.RWMutex{},
		deployedFunctions:  make(map[string]*Function),
		storageManager: storageManager,
		metricMu: sync.RWMutex{},
		coldStartTime: make([]int64, 0),
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
		fs.deployedFunctions[fn.name] = &fn
	}
	// log.Println(fs.deployedFunctions)

	return &fs, nil
}

/**
 * Cleanup function replicas that are not being used
 */
func (fs *FunctionStore) CleanupDaemon(client *containerd.Client, cni gocni.CNI) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

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
				deleteReplica(ctx, client, *fs.deployedFunctions[name], cni, cn);
			}
		}
	}
}

/* Gets a function's metadata from DB, creates/populates Function struct
 * and returns pointer to that struct. 
 */
func (fs *FunctionStore) GetDeployedFunctions() ([]Function, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
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
func (fs *FunctionStore) GetDeployedFunction(name string) (fn Function, err error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
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
	fs.mu.Lock()
	fs.deployedFunctions[fn.name] = fn
	fs.mu.Unlock()
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
	fs.mu.RLock()
	fn := fs.deployedFunctions[name]
	fs.mu.RUnlock()

	err := fs.storageManager.DeleteFunction(name)
	if err != nil {
		return err
	}
	for _, c := range getContainers(fn) {
		fs.storageManager.DeleteContainer(c.Name)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.deployedFunctions[name]; ok {
		delete(fs.deployedFunctions, name)
		log.Printf("[removeDeployedFunction] Removed deployed function '%s'", name)
		return nil
	}
	return fmt.Errorf("[removeDeployedFunction] Unable to locate function %s", name)
}

/* Gets the container name of an idle (warm) Function replica and sets the
 * container replica to Active */
func (fs *FunctionStore) GetIdleReplica(fn string) (string, string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

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
func (fs *FunctionStore) AddActiveReplica(fn string, cn string, ip string) error {
	err := fs.storageManager.InsertContainer(storage.Container{Name: cn, ParentFunction: fn, Ip: ip})
	if err != nil {
		return err
	}
	fs.mu.Lock()
	fs.deployedFunctions[fn].activeReplicas[cn] = ip
	fs.mu.Unlock()

	return nil
}

/* Change container from active to inactive
 * Note: lookup by ip and container name is not avaiable in resolver 
 */
func (fs *FunctionStore) UpdateReplicaStatusInactive(fn string, ip string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

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

func (fs *FunctionStore) RecordColdStartTime(time time.Duration) {
	fs.metricMu.Lock()
	defer fs.metricMu.Unlock()

	fs.coldStartTime = append(fs.coldStartTime, time.Milliseconds())
}

func (fs *FunctionStore) GetColdStartTimes() ([]int64){
	fs.metricMu.RLock()
	defer fs.metricMu.RUnlock()

	return fs.coldStartTime
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
		name: f.Name,
		namespace: f.Namespace,
		image: f.Image,
		labels: labels,
		idleReplicas: make(map[string]string),
		idleReplicasTs: make(map[string]time.Time),
		activeReplicas: make(map[string]string),
		annotations: annotations,
		secrets: secrets,
		secretsPath: f.SecretsPath,
		envVars: envVars,
		envProcess: f.EnvProcess,
		memoryLimit: f.MemoryLimit,
	}
}

func getStorageFunction(f *Function) storage.Function {
	lables, _ := json.Marshal(f.labels)
	annotations, _ := json.Marshal(f.annotations)
	envVars, _ := json.Marshal(f.envVars)
	secrets, _ := json.Marshal(f.secrets)

	return storage.Function{
		Name: f.name,
		Namespace: f.namespace,
		Image: f.image,
		Labels: string(lables),
		Annotations: string(annotations),
		Secrets: string(secrets),
		SecretsPath: f.secretsPath,
		EnvVars: string(envVars),
		EnvProcess: f.envProcess,
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