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
)

const watchdogPort = 8080

type InvokeResolver struct {
	client *containerd.Client
	cni    gocni.CNI
	fs     *FunctionStore
}

func NewInvokeResolver(client *containerd.Client, cni gocni.CNI, fs *FunctionStore) *InvokeResolver {
	return &InvokeResolver{client: client, cni: cni, fs: fs}
}

func (i *InvokeResolver) Resolve(functionName string, requestID string, userID string) (url.URL, bool, error) {
	defer timec.RecordDuration("(invoke_resolver.go) Resolve() <requestID="+requestID+">", time.Now())
	actualFunctionName := functionName
	log.Printf("Resolve: %q (requestID=%s)\n", actualFunctionName, requestID)

	namespace := getNamespace(functionName, fecore.DefaultFunctionNamespace)

	if strings.Contains(functionName, ".") {
		actualFunctionName = strings.TrimSuffix(functionName, "."+namespace)
	}

	/* TODO: Eventually we want to update GetFunction() to return a pointer to
	 * the struct for actualFunctionName; for now, we'll access the
	 * DeployedFunctions map directly */
	function, er := i.fs.GetDeployedFunction(actualFunctionName, requestID)
	if er != nil {
		log.Printf("[Invoke] Error retrieving function '%s': %s", actualFunctionName, er)
		return url.URL{}, false, er
	}

	var replicaName string
	var replicaIP string
	var err error
	var coldStart bool
	/* Get the next idle replica if there is at least 1 available (warm start);
	 * otherwise create new replica (cold start) */
	/* TODO: Potential race condition here: function.idleReplicas is read w/o mutex */
	if len(function.idleReplicas) > 0 {
		coldStart = false
		replicaName, replicaIP, err = i.fs.GetIdleReplica(actualFunctionName, requestID)
		if err != nil {
			log.Printf("[Invoke] Error retrieving idle replica for Function '%s': %s", function.name, err)
			return url.URL{}, false, err
		}
		log.Printf("[Invoke] Using idle replica '%s' (%s) for Function '%s' <requestID=%s>", replicaName, replicaIP, function.name, requestID)
	} else {
		coldStart = true
		log.Printf("[Invoke] Creating new replica for Function '%s' <requestID=%s>", function.name, requestID)
		startTime := time.Now()
		replicaName, err := createReplica(i.client, i.cni, i.fs, &function, requestID, true)
		if err != nil {
			log.Printf("[Invoke] Error creating new replica for Function %s: %s", function.name, err)
			return url.URL{}, false, err
		} else {
			timec.RecordDuration("(invoke_resolver.go).Resolve.[CreateReplicaTime] <requestID="+requestID+">", startTime)
			coldStartTime := time.Since(startTime)
			replicaIP = function.activeReplicas[replicaName]
			i.fs.AddActiveReplica(function.name, replicaName, replicaIP, requestID)
			i.fs.RecordColdStartTime(coldStartTime, requestID)
			log.Printf("[Invoke] Using new replica '%s' (%s) for Function '%s' <requestID=%s>", replicaName, replicaIP, function.name, requestID)
		}
	}
	// TODO: State management: add new association between user and replica
	i.fs.MarkUserToReplica(function.name, userID, replicaName, fecore.StrongStickiness)
	log.Printf("[Invoke] Mark stickiness '%s': (%s) for Function '%s' <Stickiness=%d>", userID, replicaName, function.name, fecore.StrongStickiness)

	urlStr := fmt.Sprintf("http://%s:%d", replicaIP, watchdogPort)

	urlRes, err := url.Parse(urlStr)
	if err != nil {
		return url.URL{}, false, err
	}

	return *urlRes, coldStart, nil
}

func getNamespace(name, defaultNamespace string) string {
	namespace := defaultNamespace
	if strings.Contains(name, ".") {
		namespace = name[strings.LastIndexAny(name, ".")+1:]
	}
	return namespace
}
