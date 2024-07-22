package proxy

import (
	"fmt"
	"log"
	"net/http"

	"github.gatech.edu/faasedge/fecore/pkg/provider/handlers"
)

// isLastFunction checks if the given function is the last function in the workflow
func getDonwstreamFunction(functionName string, wf *handlers.Workflow) string {
	for _, fi := range wf.FunctionSequence {
		if fi.FunctionName == functionName {
			return fi.Downstream
		}
	}
	return ""
}

// sendDownstreamRequest sends the response to the next function in the workflow
func sendDownstreamRequest(functionName, workflowID, vDAGID, headerContentType, requestID string, response *http.Response) error {
	// Retrieve the workflow
	wf, exists := handlers.GetWorkflow(workflowID)
	if !exists {
		return fmt.Errorf("workflow with ID %s not found", workflowID)
	}
	downstreamFunction := getDonwstreamFunction(functionName, &wf)

	if downstreamFunction == "" {
		// Send response back to stateflow
		// TODO: harded coded for now; move it to healthcheck registration.
		nextURL := "http://128.110.217.72:8088/response"
		req, err := http.NewRequest("POST", nextURL, response.Body)
		if err != nil {
			return fmt.Errorf("failed to create request to stateflow: %v", err)
		}
		req.Header.Set("Content-Type", headerContentType)
		req.Header.Set("firstRequestID", requestID)
		req.Header.Set("vDAGID", vDAGID)
		req.Header.Set("workflowID", workflowID)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send response to stateflow: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to get response from stateflow: %v", resp.Status)
		}

		log.Printf("[Workflow Execution] Successfully sent response to stateflow for function %s in workflow %s", functionName, workflowID)
		return nil
	}

	// Send response to the next function in the workflow
	nextFecoreAddr, exists := wf.VDAGsMap[vDAGID][downstreamFunction]
	if !exists {
		return fmt.Errorf("next fecore address not found for function %s in workflow %s", downstreamFunction, workflowID)
	}

	nextURL := fmt.Sprintf("http://%s:%d/function/%s", nextFecoreAddr.IP, nextFecoreAddr.Port, downstreamFunction)
	req, err := http.NewRequest("POST", nextURL, response.Body)
	if err != nil {
		return fmt.Errorf("failed to create request to next function: %v", err)
	}
	req.Header.Set("Content-Type", headerContentType)
	req.Header.Set("firstRequestID", requestID)
	req.Header.Set("vDAGID", vDAGID)
	req.Header.Set("workflowID", workflowID)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to next function: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to send request to next function: %v", resp.Status)
	}

	log.Printf("[sendDownstreamRequest] Downstream successfully processes request to next function %s in workflow %s", downstreamFunction, workflowID)
	return nil
}
