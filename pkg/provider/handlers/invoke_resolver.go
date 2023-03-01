package handlers

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/containerd/containerd"
	gocni "github.com/containerd/go-cni"
	fecore "github.gatech.edu/faasedge/fecore/pkg"
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

func (i *InvokeResolver) Resolve(functionName string) (url.URL, error) {
	actualFunctionName := functionName
	log.Printf("Resolve: %q\n", actualFunctionName)

	namespace := getNamespace(functionName, fecore.DefaultFunctionNamespace)

	if strings.Contains(functionName, ".") {
		actualFunctionName = strings.TrimSuffix(functionName, "."+namespace)
	}

	/* TODO: Eventually we want to update GetFunction() to return a pointer to
	 * the struct for actualFunctionName; for now, we'll access the
	 * DeployedFunctions map directly */
	function, er := i.fs.GetDeployedFunction(actualFunctionName)
	if er != nil {
		log.Printf("[Invoke] Error retrieving function '%s': %s", actualFunctionName, er)
		return url.URL{}, er
	}

	var replicaName string
	var replicaIP string
	var err error
	/* Get the next idle replica if there is at least 1 available (warm start);
	 * otherwise create new replica (cold start) */
	if len(function.idleReplicas) > 0 {
		replicaName, replicaIP, err = i.fs.GetIdleReplica(actualFunctionName)
		if err != nil {
			log.Printf("[Invoke] Error retrieving idle replica for Function '%s': %s", function.name, err)
			return url.URL{}, err
		}
		log.Printf("[Invoke] Using idle replica '%s' for Function '%s'", replicaName, function.name)
	} else {
		log.Printf("[Invoke] Creating new replica for Function '%s'", function.name)
		replicaName, err := createReplica(i.client, i.cni, &function, true)
		if err != nil {
			log.Printf("[Invoke] Error creating new replica for Function %s: %s", function.name, err)
			return url.URL{}, err
		} else {
			replicaIP = function.activeReplicas[replicaName]
			i.fs.AddActiveReplica(function.name, replicaName, replicaIP)
			log.Printf("[Invoke] Using new replica '%s' @ %s for Function '%s'", replicaName, replicaIP, function.name)
		}
	}

	urlStr := fmt.Sprintf("http://%s:%d", replicaIP, watchdogPort)

	urlRes, err := url.Parse(urlStr)
	if err != nil {
		return url.URL{}, err
	}

	return *urlRes, nil
}

func getNamespace(name, defaultNamespace string) string {
	namespace := defaultNamespace
	if strings.Contains(name, ".") {
		namespace = name[strings.LastIndexAny(name, ".")+1:]
	}
	return namespace
}
