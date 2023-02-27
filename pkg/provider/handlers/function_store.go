package handlers

import (
	"fmt"
	"log"
)

/*
 * All read functions return copies and not references
 * All write functions should update the storage
 */

/* In-memory map for keeping track of functions that have been created */
type FunctionStore struct {
	/* TODO: Add per-Function mutex for protecting Function modification */
	DeployedFunctions map[string]*Function
}

func InitFunctionStore() (*FunctionStore, error) {
	fs := FunctionStore {
		DeployedFunctions:  make(map[string]*Function),
	}
	// TODO init map from sql
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

func (fs *FunctionStore) AddDeployedFunction(fn *Function) {
	/* TODO: Add code to write deployed function to DB;
	 * for now it stays in-memory via DeployedFunctions map */
	fs.DeployedFunctions[fn.name] = fn
	log.Printf("[addDeployedFunction] Added deployed function '%s' in namespace '%s'", fn.name, fn.namespace)
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

/* Updates a function's metadata in DB */
func (fs *FunctionStore) UpdateDeployedFunction(fn *Function) error {
	log.Printf("[updateDeployedFunction] Updated metadata for deployed function '%s'", fn.name)
	return nil
}

/* Remove function from DB */
func (fs *FunctionStore) RemoveDeployedFunction(name string) error {
	/* TODO: Add code to remove Function from DB */

	/* For now, remove Function from in-memory map */
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
