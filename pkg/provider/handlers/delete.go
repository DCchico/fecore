package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"
	"github.com/openfaas/faas/gateway/requests"

	cninetwork "github.gatech.edu/faasedge/fecore/pkg/cninetwork"
	"github.gatech.edu/faasedge/fecore/pkg/service"
)

func MakeDeleteHandler(client *containerd.Client, cni gocni.CNI, fs *FunctionStore) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := io.ReadAll(r.Body)
		log.Printf("[Delete] request: %s\n", string(body))

		req := requests.DeleteFunctionRequest{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Delete] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(client.NamespaceService(), lookupNamespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		name := req.FunctionName

		ctx := namespaces.WithNamespace(context.Background(), lookupNamespace)

		// Remove each Function replica
		fn, err := fs.GetDeployedFunction(name, "Delete <"+name+">")
		fs.dfMu.Lock()
		for rname := range fn.idleReplicas {
			deleteReplica(ctx, client, fn, cni, rname)
		}
		for rname := range fn.activeReplicas {
			deleteReplica(ctx, client, fn, cni, rname)
		}
		fs.dfMu.Unlock()
		fs.RemoveDeployedFunction(name)

		log.Printf("[Delete] Deleted Function '%s'\n", name)
	}
}

func deleteReplica(ctx context.Context, client *containerd.Client, fn Function, cni gocni.CNI, name string) error {

	// TODO refactor, two different paths for wasm and regular containers
	if val, ok := fn.labels["module.wasm.image/variant"]; ok && (val == "compat-smart" || val == "compat") {
		networkErr := cninetwork.DeleteWasmCNINetwork(ctx, cni, client, name, int(fn.pid[name]))
		if networkErr != nil {
			log.Printf("[deleteReplica] Error removing network for Function '%s': %s\n", name, networkErr)
		}
		pid := int(fn.pid[name])
		proc, err := os.FindProcess(pid)
		if err != nil {
			log.Printf("[deleteReplica] Could not find WASM container process (PID=%d)", pid)
			return err
		} else {
			proc.Kill()
			log.Printf("[deleteReplica] Killed WASM container process (PID=%d)", pid)
		}
		err = os.RemoveAll(fn.image + "/replicas/" + name)
		if err != nil {
			log.Printf("[deleteReplica] Could not delete rootfs for container replica %s", name)
			return err
		} else {
			log.Printf("[deleteReplica]  Deleted rootfs for container replica %s", name)
		}
	} else {
		networkErr := cninetwork.DeleteCNINetwork(ctx, cni, client, name)
		if networkErr != nil {
			log.Printf("[deleteReplica] Error removing network for Function '%s': %s\n", name, networkErr)
		}
		containerErr := service.Remove(ctx, client, name)
		if containerErr != nil {
			log.Printf("[deleteReplica] Error removing replica container '%s': %s\n", name, containerErr)
			return containerErr
		}
	}
	log.Printf("[deleteReplica] Removed replica container: '%s'\n", name)
	return nil
}
