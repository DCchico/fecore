package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/openfaas/faas-provider/types"
)

type FecoreAddress struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type FunctionState struct {
	StateName string `json:"stateName"`
	VDAGonly  bool   `json:"vDAGonly"`
}

type FunctionInvocation struct {
	FunctionName   string                   `json:"functionName"`
	Downstream     string                   `json:"downstream"`
	FunctionStates []FunctionState          `json:"functionstates"`
	Parameters     types.FunctionDeployment `json:"parameters"`
}

type Workflow struct {
	ID               string                              `json:"id"`
	VDAGs            []string                            `json:"vDAGs"`
	FunctionSequence []FunctionInvocation                `json:"functionSequence"`
	VDAGsMap         map[string]map[string]FecoreAddress `json:"vDAGsMap"`
}

var (
	mu        sync.RWMutex
	workflows = make(map[string]Workflow)
)

// RegisterWorkflow adds a new workflow to the map
func RegisterWorkflow(wf Workflow) {
	mu.Lock()
	defer mu.Unlock()
	workflows[wf.ID] = wf
	log.Printf("[Fecore: Workflow] Registered workflow with ID: %s", wf.ID)
}

// GetWorkflow retrieves a workflow by its ID
func GetWorkflow(id string) (Workflow, bool) {
	mu.RLock()
	defer mu.RUnlock()
	wf, exists := workflows[id]
	return wf, exists
}

// MakeWorkflowHandler returns an HTTP handler function that registers a workflow
func MakeWorkflowHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var wf Workflow
		if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
			http.Error(w, "Invalid workflow registration request", http.StatusBadRequest)
			return
		}

		RegisterWorkflow(wf)

		log.Printf("[Workflow] Workflow registered: %v", wf)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "workflow registered"})
	}
}
