package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	gocni "github.com/containerd/go-cni"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/openfaas/faas-provider/types"
	cninetwork "github.gatech.edu/faasedge/fecore/pkg/cninetwork"
	"github.gatech.edu/faasedge/fecore/pkg/service"
)

func MakeReplicaReaderHandler(client *containerd.Client, fs *FunctionStore) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		functionName := vars["name"]
		lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))

		// Check if namespace exists, and it has the proper label
		valid, err := validNamespace(client.NamespaceService(), lookupNamespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		/* TODO: Look into how 'found' JSON is used by gateway to call Functions
		 * (esp. wrt the 'AvailableReplicas' field).
		 * For now, hardcoding AvailableReplicas to a high number tricks the
		 * Gateway's scaling logic into sending over the request w/o delay.
		 * We want to implement explicit scaling requests in the future, but
		 * for now we'll handle scaling interally via invoke_resolver.go */
		if f, err := fs.GetDeployedFunction(functionName); err == nil {
			found := types.FunctionStatus{
				Name:              functionName,
				Image:             f.image,
				AvailableReplicas: 99,
				Replicas:          uint64(f.replicas + 99),
				Namespace:         f.namespace,
				Labels:            &f.labels,
				Annotations:       &f.annotations,
				Secrets:           f.secrets,
				EnvVars:           f.envVars,
				EnvProcess:        f.envProcess,
				CreatedAt:         f.createdAt,
			}

			functionBytes, _ := json.Marshal(found)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(functionBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func createReplica(client *containerd.Client, cni gocni.CNI, fn *Function, setActive bool) (string, error) {

	ctx := namespaces.WithNamespace(context.Background(), fn.namespace)

	snapshotter := ""
	if val, ok := os.LookupEnv("snapshotter"); ok {
		snapshotter = val
	}

	image, err := service.PrepareImage(ctx, client, fn.image, snapshotter, false)
	if err != nil {
		return "", fmt.Errorf("[createReplica] Unable to pull image %s", fn.image)
	}

	envs := prepareEnv(fn.envProcess, fn.envVars)

	mounts := getOSMounts()

	for _, secret := range fn.secrets {
		mounts = append(mounts, specs.Mount{
			Destination: path.Join("/var/openfaas/secrets", secret),
			Type:        "bind",
			Source:      path.Join(fn.secretsPath, secret),
			Options:     []string{"rbind", "ro"},
		})
	}

	name := fn.name + "-" + uuid.New().String()

	labels := fn.labels

	var memory *specs.LinuxMemory
	memory = &specs.LinuxMemory{}
	memory.Limit = &fn.memoryLimit

	log.Printf("[createReplica] Creating new container for Function '%s'", name)
	container, err := client.NewContainer(
		ctx,
		name,
		containerd.WithImage(image),
		containerd.WithSnapshotter(snapshotter),
		containerd.WithNewSnapshot(name+"-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image),
			oci.WithHostname(name),
			oci.WithCapabilities([]string{"CAP_NET_RAW"}),
			oci.WithMounts(mounts),
			oci.WithEnv(envs),
			withMemory(memory)),
		containerd.WithContainerLabels(labels),
	)

	if err != nil {
		return "", fmt.Errorf("[createReplica] Unable to create container '%s': %w", name, err)
	}

	createTaskStatus := createTask(ctx, container, cni)
	if createTaskStatus != nil {
		return "", fmt.Errorf("[createReplica] Unable to create task for container '%s': %w", name, err)
	}
	task, err := container.Task(ctx, nil)
	if err == nil {
		// Task for container exists
		_, err := task.Status(ctx)
		if err != nil {
			return "", fmt.Errorf("[createReplica] Unable to get task status for container '%s': %w", name, err)
		}
		ip, err := cninetwork.GetIPAddress(name, task.Pid())
		if err != nil {
			return "", fmt.Errorf("[createReplica] Unable to get IP for container '%s': %w", name, err)
		}
		if setActive {
			fn.activeReplicas[name] = ip
		} else {
			fn.idleReplicas[name] = ip
		}
		return name, nil
	}
	return "", err
}
