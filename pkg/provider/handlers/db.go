package handlers

import (
	"fmt"
	"log"
)

/* TODO: Add per-Function mutex for protecting Function modification */

/* In-memory map for keeping track of functions that have been created
 * May replace this w/ SQLite DB later */
var DeployedFunctions = map[string]*Function{}

func addDeployedFunction(fn *Function) {
	/* TODO: Add code to write deployed function to DB;
	 * for now it stays in-memory via DeployedFunctions map */
	DeployedFunctions[fn.name] = fn
	log.Printf("[addDeployedFunction] Added deployed function '%s' in namespace '%s'", fn.name, fn.namespace)
}

/* Gets a function's metadata from DB, creates/populates Function struct
 * and returns pointer to that struct */
func getDeployedFunction(name string) (fn *Function, err error) {
	fn = &Function{}
	fn.name = name
	/* TODO: Add code to retrieve Function with 'name' from DB;
	 * If successful, populate fields of fn with metadata and
	 * return (fn,nil); otherwise, return (nil, error) */
	return fn, nil
}

/* Updates a function's metadata in DB */
func updateDeployedFunction(fn *Function) error {
	log.Printf("[updateDeployedFunction] Updated metadata for deployed function '%s'", fn.name)
	return nil
}

/* Remove function from DB */
func removeDeployedFunction(name string) error {
	/* TODO: Add code to remove Function from DB */

	/* For now, remove Function from in-memory map */
	if _, ok := DeployedFunctions[name]; ok {
		delete(DeployedFunctions, name)
		log.Printf("[removeDeployedFunction] Removed deployed function '%s'", name)
		return nil
	}
	return fmt.Errorf("[removeDeployedFunction] Unable to locate function %s", name)
}

/* Gets the container name of an idle (warm) Function replica and sets the
 * container replica to Active */
func getIdleReplica(fn string) (string, string, error) {
	var name string
	var ip string
	for k, v := range DeployedFunctions[fn].idleReplicas {
		name = k
		ip = v
		break
	}
	delete(DeployedFunctions[fn].idleReplicas, name)
	DeployedFunctions[fn].activeReplicas[name] = ip
	return name, ip, nil
}
