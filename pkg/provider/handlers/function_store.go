package handlers

import (
	"fmt"
	"log"
	"encoding/json"

	"github.gatech.edu/faasedge/fecore/pkg/provider/storage"
)

/*
 * All read functions return copies and not references
 * All write functions should update the storage
 */

/* In-memory map for keeping track of functions that have been created */
type FunctionStore struct {
	/* TODO: Add per-Function mutex for protecting Function modification */
	DeployedFunctions map[string]*Function
	StorageManager storage.StorageManager
}

func InitFunctionStore(storageManager storage.StorageManager) (*FunctionStore, error) {
	fs := FunctionStore {
		DeployedFunctions:  make(map[string]*Function),
		StorageManager: storageManager,
	}

	sFns, err := storageManager.GetAllFunctions()
	if err != nil {
		return nil, err
	}
	
	log.Println(sFns)
	for _, sFn := range sFns {
		fn := getFunction(&sFn)
		cns, err := storageManager.GetContainersForFunction(fn.name)
		log.Println(cns)
		if err != nil {
			return nil, err
		}
		for _, c := range cns {
			fn.idleReplicas[c.Name] = c.Ip
		}
		fs.DeployedFunctions[fn.name] = &fn
	}
	log.Println(fs.DeployedFunctions)

	return &fs, nil
}

/* Gets a function's metadata from DB, creates/populates Function struct
 * and returns pointer to that struct. 
 */
func (fs *FunctionStore) GetDeployedFunctions() ([]Function, error) {
	fns := make([]Function, 0)
	for _, value := range fs.DeployedFunctions {
		fns = append(fns, *value)
	}
	return fns, nil
}

/* Gets a function's metadata from DB, creates/populates Function struct
 * and returns pointer to that struct 
 * TODO check namespace 
 */
func (fs *FunctionStore) GetDeployedFunction(name string) (fn Function, err error) {
	if fn, ok := fs.DeployedFunctions[name]; ok {
		return *fn, nil
	}
	return Function{}, fmt.Errorf("[GetDeployedFunction] Unable to get function from `store %s", name)
}

/* Add deployed function to the store */
func (fs *FunctionStore) AddDeployedFunction(fn *Function) error {
	fs.DeployedFunctions[fn.name] = fn
	err := fs.StorageManager.InsertFunction(getStorageFunction(fn))
	if err != nil {
		return err
	}
	for _, c := range getContainers(fn) {
		fs.StorageManager.InsertContainer(c)
	}
	if err != nil {
		return err
	}
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
	fn := fs.DeployedFunctions[name]
	err := fs.StorageManager.DeleteFunction(name)
	if err != nil {
		return err
	}
	for _, c := range getContainers(fn) {
		fs.StorageManager.DeleteContainer(c.Name)
	}

	if _, ok := fs.DeployedFunctions[name]; ok {
		delete(fs.DeployedFunctions, name)
		log.Printf("[removeDeployedFunction] Removed deployed function '%s'", name)
		return nil
	}
	return fmt.Errorf("[removeDeployedFunction] Unable to locate function %s", name)
}

/* Gets the container name of an idle (warm) Function replica and sets the
 * container replica to Active */
func (fs *FunctionStore) GetIdleReplica(fn string) (string, string, error) {
	var name string
	var ip string
	for k, v := range fs.DeployedFunctions[fn].idleReplicas {
		name = k
		ip = v
		break
	}
	delete(fs.DeployedFunctions[fn].idleReplicas, name)
	fs.DeployedFunctions[fn].activeReplicas[name] = ip
	return name, ip, nil
}

/* Adds new active replica */
func (fs *FunctionStore) AddActiveReplica(fn string, cn string, ip string) error {
	fs.DeployedFunctions[fn].activeReplicas[cn] = ip
	err := fs.StorageManager.InsertContainer(storage.Container{Name: cn, ParentFunction: fn, Ip: ip})
	if err != nil {
		return err
	}
	return nil
}

/* Change container from active to inactive
 * Note: lookup by ip and container name is not avaiable in resolver 
 */
func (fs *FunctionStore) UpdateReplicaStatusInactive(fn string, ip string) error {
	cn := ""
	for c, cIp := range fs.DeployedFunctions[fn].activeReplicas {
		if cIp == ip {
			cn = c
			break
		}
	}
	if cn == "" {
		return fmt.Errorf("[UpdateReplicaStatusInactive] Unable to get function %s container %s in active set", fn, cn)
	}
	delete(fs.DeployedFunctions[fn].activeReplicas, cn)
	fs.DeployedFunctions[fn].idleReplicas[cn] = ip	
	return nil
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