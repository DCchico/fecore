package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime"

	"github.com/containerd/containerd"
	gocni "github.com/containerd/go-cni"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

type NodeCondition string

const (
	Ready          NodeCondition = "Ready"
	OutOfMemory    NodeCondition = "OutOfMemory"
	MemoryPressure NodeCondition = "MemoryPressure"
	CPUPressure    NodeCondition = "CPUPressure"
	OutOfCPU       NodeCondition = "OutOfCPU"
)

type HealthStatus struct {
	Conditions      []NodeCondition `json:"conditions"`
	TotalMemory     uint64          `json:"total_memory"`
	AvailableMemory uint64          `json:"available_memory"`
	TotalCPU        int             `json:"total_cpu"`
	AvailableCPU    float64         `json:"available_cpu"`
}

func bytesToGB(bytes uint64) float64 {
	return float64(bytes) / 1073741824
}

func getNodeConditions() ([]NodeCondition, HealthStatus) {
	var conditions []NodeCondition
	var healthStatus HealthStatus

	// Check memory pressure
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("[HealthCheck] Error getting virtual memory stats: %v", err)
		conditions = append(conditions, OutOfMemory)
	} else {
		healthStatus.TotalMemory = vmStat.Total
		healthStatus.AvailableMemory = vmStat.Available
		log.Printf("[HealthCheck] Total Memory: %.2f GB, Available Memory: %.2f GB", bytesToGB(healthStatus.TotalMemory), bytesToGB(healthStatus.AvailableMemory))
		if vmStat.Available < vmStat.Total/10 { // Example condition: less than 10% memory available
			conditions = append(conditions, MemoryPressure)
			log.Println("[HealthCheck] Current Memory Condition: MemoryPressure")
		}
	}

	// Check CPU pressure
	cpuStats, err := cpu.Percent(0, false)
	if err != nil {
		log.Printf("[HealthCheck] Error getting CPU stats: %v", err)
		conditions = append(conditions, OutOfCPU)
	} else {
		healthStatus.TotalCPU = runtime.NumCPU()
		usedCPUPercentage := cpuStats[0]
		availableCPUFraction := float64(healthStatus.TotalCPU) * (1 - usedCPUPercentage/100)
		healthStatus.AvailableCPU = availableCPUFraction
		log.Printf("[HealthCheck] Total CPU: %d cores, Available CPU: %.2f cores", healthStatus.TotalCPU, healthStatus.AvailableCPU)
		if healthStatus.AvailableCPU < float64(healthStatus.TotalCPU)*0.2 { // Example condition: less than 20% CPU available
			conditions = append(conditions, CPUPressure)
			log.Println("[HealthCheck] Current CPU Condition: CPUPressure")
		}
	}
	// Get Function Replicas
	// fns := fs.GetDeployedFunctions()
	// for _, fn =: range fns {

	// }

	// Check if the node is ready (this is an example, customize as needed)
	conditions = append(conditions, Ready)

	healthStatus.Conditions = conditions
	return conditions, healthStatus
}

// MakeHealthCheckHandler returns a handler to check the health of the node
func MakeHealthCheckHandler(client *containerd.Client, cni gocni.CNI, fs *FunctionStore) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("[HealthCheck] Received health check request")
		// Extract Stateflow IP and Port from headers
		stateflowIP := r.Header.Get("Stateflow-IP")
		stateflowPort := r.Header.Get("Stateflow-Port")
		if stateflowIP != "" && stateflowPort != "" {
			log.Printf("[HealthCheck] Received Stateflow IP: %s and Port: %s", stateflowIP, stateflowPort)
			fs.dfMu.Lock()
			fs.stateflowIP = stateflowIP
			fs.stateflowPort = stateflowPort
			fs.dfMu.Unlock()
		}
		_, healthStatus := getNodeConditions()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(healthStatus)
		if err != nil {
			log.Printf("[HealthCheck] Error encoding health status to JSON: %v", err)
		} else {
			log.Println("[HealthCheck] Health status response sent")
		}
	}
}
