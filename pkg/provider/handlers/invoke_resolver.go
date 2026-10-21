package handlers

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/containerd/containerd"
	gocni "github.com/containerd/go-cni"
	fecore "github.gatech.edu/faasedge/fecore/pkg"
	"github.gatech.edu/faasedge/fecore/pkg/timec"
	"github.gatech.edu/faasedge/fecore/pkg/semaphore"
)

const watchdogPort = 8080



type InvokeResolver struct {
	client *containerd.Client
	cni    gocni.CNI
	fs     *FunctionStore
}

// Define DependencyInfo struct to match the JSON structure
type DependencyInfo struct {
	SenderVDAG   string `json:"sender_vdag"`
	SenderSeq    string    `json:"sender_seq"`
	FecoreIP     string `json:"fecore_ip"`
	FecorePort   string    `json:"fecore_port"`
}

// DataCentricDependency represents the parsed data-centric dependency information
type DataCentricDependency struct {
	SenderVDAG  string
	SenderSeq   string
	Key         string
}

func parseDataCentricDependency(dep string) (*DataCentricDependency, error) {
	parts := strings.Split(dep, "+")
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid data-centric dependency format: expected 5 parts, got %d", len(parts))
	}
	
	return &DataCentricDependency{
		SenderVDAG: parts[1],
		SenderSeq:  parts[2],
		Key:        parts[4],
	}, nil
}

func NewInvokeResolver(client *containerd.Client, cni gocni.CNI, fs *FunctionStore) *InvokeResolver {
	return &InvokeResolver{client: client, cni: cni, fs: fs}
}

func (i *InvokeResolver) Resolve(functionName string, requestID string, dependencies []DependencyInfo, vDAGID string, 
	sequenceID string, releaseLocalState bool, reserveLocalState bool, dataCentricDependency string) (url.URL, bool, bool, error) {

	defer timec.RecordDuration("(invoke_resolver.go) Resolve() <requestID="+requestID+">", time.Now())
	actualFunctionName := functionName
	log.Printf("[Resolve] Resolving <function=%q, vDAGID=%s, sequenceID=%s> (requestID=%s)\n", actualFunctionName, vDAGID, sequenceID, requestID)

	// Handle dependencies if provided
	if len(dependencies) > 0 {
		dep := dependencies[0]
		log.Printf("[Resolve] Handling dependency direct mode - SenderVDAG: %s, SenderSeq: %s", dep.SenderVDAG, dep.SenderSeq)
		for {
			containerIP, isRunning := i.fs.GetInvocationDetails(actualFunctionName, dep.SenderVDAG, dep.SenderSeq)
			if !isRunning && containerIP != "" {
				log.Printf("[Resolve] Using idle dependent container %s - SenderVDAG: %s, SenderSeq: %s", containerIP, dep.SenderVDAG, dep.SenderSeq)
				// Use the idle container
				replicaIP := containerIP
				coldStart := false
				i.fs.RecordInvocationDetails(functionName, vDAGID, sequenceID, replicaIP, releaseLocalState, reserveLocalState)
				log.Printf("[Resolve] Recorded invocation dependency details for Function '%s' <vDAGID=%s, sequenceID=%s, containerIP=%s>", functionName, vDAGID, sequenceID, replicaIP)

				urlStr := fmt.Sprintf("http://%s:%d", replicaIP, watchdogPort)
				urlRes, err := url.Parse(urlStr)
				if err != nil {
					return url.URL{}, false, true, err
				}
				return *urlRes, coldStart, true, nil
			} else if !isRunning && containerIP == "" {
				log.Printf("[Resolve] Dependent container has not been executed yet - SenderVDAG: %s, SenderSeq: %s", dep.SenderVDAG, dep.SenderSeq)
				time.Sleep(10 * time.Millisecond)
			} else if containerIP == "Unknown" {
				log.Printf("[Resolve] Dependent container %s is unknown - SenderVDAG: %s, SenderSeq: %s", containerIP, dep.SenderVDAG, dep.SenderSeq)
				break
			} else {
				log.Printf("[Resolve] Dependent container %s is running - SenderVDAG: %s, SenderSeq: %s", containerIP, dep.SenderVDAG, dep.SenderSeq)
				time.Sleep(3 * time.Millisecond)
			}
		}
	} else if dataCentricDependency != "" {
		log.Printf("[Resolve] Handling data-centric dependency - dataCentricDependency: %s", dataCentricDependency)
		var sem *semaphore.Semaphore
		var err error
		// Parse the data-centric dependency string
		dep, err := parseDataCentricDependency(dataCentricDependency)
		if err != nil {
			log.Printf("[Resolve] Error parsing data-centric dependency: %v", err)
			return url.URL{}, false, false, err
		}

		log.Printf("[Resolve] Parsed dependency - SenderVDAG: %s, SenderSeq: %s, Key: %s",
			dep.SenderVDAG, dep.SenderSeq, dep.Key)

		// Construct semaphore name
		semName := fmt.Sprintf("/%s_%s_%s_sem", dep.Key, dep.SenderVDAG, dep.SenderSeq)
		log.Printf("[Resolve] Attempting to check semaphore: %s", semName)

		// The previous invocation creates this semaphore when its state is
		// committed. Wait for that, but do not spin forever: a name mismatch
		// used to pin the provider until it was restarted.
		deadline := time.Now().Add(30 * time.Second)
		nextLog := time.Time{}
		for {
			sem, err = semaphore.Open(semName, semaphore.O_RDWR, 0666, 0)
			if err != nil {
				if strings.Contains(err.Error(), "No such file or directory") {
					if time.Now().After(deadline) {
						log.Printf("[Resolve] Timed out waiting for semaphore %s", semName)
						return url.URL{}, false, false, fmt.Errorf("timed out waiting for data-centric semaphore %s", semName)
					}
					if time.Now().After(nextLog) {
						log.Printf("[Resolve] Semaphore %s not yet created, waiting...", semName)
						nextLog = time.Now().Add(time.Second)
					}
					time.Sleep(20 * time.Millisecond)
					continue
				}
				log.Printf("[Resolve] Error opening semaphore %s: %v", semName, err)
				return url.URL{}, false, false, fmt.Errorf("failed to open semaphore: %v", err)
			}

			// Try to acquire the semaphore to check its state
			log.Printf("[Resolve] Checking semaphore state...")
			if err := sem.Wait(); err != nil {
				log.Printf("[Resolve] Error checking semaphore %s: %v", semName, err)
				return url.URL{}, false, false, fmt.Errorf("failed to check semaphore: %v", err)
			}

			// Immediately release the semaphore back to the application
			if err := sem.Post(); err != nil {
				log.Printf("[Resolve] Error releasing semaphore %s: %v", semName, err)
				return url.URL{}, false, false, fmt.Errorf("failed to release semaphore: %v", err)
			}
			
			// Set up cleanup
			defer sem.Close()
			
			log.Printf("[Resolve] Successfully verified semaphore %s state", semName)
			break
		}
	}

	

	namespace := getNamespace(functionName, fecore.DefaultFunctionNamespace)

	if strings.Contains(functionName, ".") {
		actualFunctionName = strings.TrimSuffix(functionName, "."+namespace)
	}

	// TODO: Duplicate function name check; Could remove if control plane is bottlenecked by function store
	function, er := i.fs.GetDeployedFunction(actualFunctionName, requestID)
	if er != nil {
		log.Printf("[Invoke] Error retrieving function '%s': %s", actualFunctionName, er)
		return url.URL{}, false, false, er
	}

	var replicaName string
	var replicaIP string
	var err error
	var coldStart bool

	// Try to get an idle replica atomically
	replicaName, replicaIP, hasIdle, err := i.fs.GetIdleReplicaAtomic(actualFunctionName, requestID)
	if err != nil {
		log.Printf("[Invoke] Error retrieving idle replica for Function '%s': %s", function.name, err)
		return url.URL{}, false, false, err
	}

	if hasIdle {
		coldStart = false
		log.Printf("[Invoke] Using idle replica '%s' (%s) for Function '%s' <requestID=%s>", replicaName, replicaIP, function.name, requestID)
	} else {
		coldStart = true
		log.Printf("[Invoke] Creating new replica for Function '%s' <requestID=%s>", function.name, requestID)
		startTime := time.Now()
		replicaName, err = createReplica(i.client, i.cni, i.fs, &function, requestID, true)
		if err != nil {
			log.Printf("[Invoke] Error creating new replica for Function %s: %s", function.name, err)
			return url.URL{}, false, false, err
		}
		timec.RecordDuration("(invoke_resolver.go).Resolve.[CreateReplicaTime] <requestID="+requestID+">", startTime)
		coldStartTime := time.Since(startTime)
		i.fs.dfMu.RLock()
		replicaIP = function.activeReplicas[replicaName]
		i.fs.dfMu.RUnlock()
		i.fs.AddActiveReplica(function.name, replicaName, replicaIP, requestID)
		i.fs.RecordColdStartTime(coldStartTime, requestID)
		log.Printf("[Invoke] Using new replica '%s' (%s) for Function '%s' <requestID=%s>", replicaName, replicaIP, function.name, requestID)
	}

	// Tag if the replica is initialized
	isInitialized := i.fs.IsReplicaInitialized(function.name, replicaIP)

	// Record the invocation details
	i.fs.RecordInvocationDetails(functionName, vDAGID, sequenceID, replicaIP, releaseLocalState, reserveLocalState)
	log.Printf("[Resolve] Recorded invocation dependency details for Function '%s' <vDAGID=%s, sequenceID=%s, containerIP=%s>", functionName, vDAGID, sequenceID, replicaIP)

	urlStr := fmt.Sprintf("http://%s:%d", replicaIP, watchdogPort)
	urlRes, err := url.Parse(urlStr)
	if err != nil {
		return url.URL{}, false, false, err
	}

	return *urlRes, coldStart, isInitialized, nil
}

func getNamespace(name, defaultNamespace string) string {
	namespace := defaultNamespace
	if strings.Contains(name, ".") {
		namespace = name[strings.LastIndexAny(name, ".")+1:]
	}
	return namespace
}
