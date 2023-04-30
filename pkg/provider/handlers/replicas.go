package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"

	"github.com/KarpelesLab/reflink"
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

	// if wasm container, we need to mount some paths and set env variables for wasmedge to load WASI-NN related plugins
	if val, ok := labels["module.wasm.image/variant"]; ok && (val == "compat-smart" || val == "compat") {
		mounts = append(mounts, specs.Mount{
			Destination: path.Join("/lib/"),
			Type:        "bind",
			Source:      path.Join("/usr/local/lib/"),
			Options:     []string{"rbind", "ro"},
		})
		mounts = append(mounts, specs.Mount{
			Destination: path.Join("/wasmedge/"),
			Type:        "bind",
			Source:      path.Join("/usr/local/lib/wasmedge/"),
			Options:     []string{"rbind", "ro"},
		})

		envs = append(envs, "WASMEDGE_PLUGIN_PATH=/wasmedge/")
		envs = append(envs, "LD_LIBRARY_PATH=\"/lib/:/wasmedge/\"")
	}

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
			oci.WithAnnotations(labels),
			oci.WithEnv(envs),
			withMemory(memory)),
		containerd.WithContainerLabels(labels),
	)

	if err != nil {
		return "", fmt.Errorf("[createReplica] Unable to create container '%s': %w", name, err)
	}

	createTaskStatus := createTask(ctx, container, cni)
	if createTaskStatus != nil {
		return "", fmt.Errorf("[createReplica] Unable to create task for container '%s': %w", name, createTaskStatus)
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

func setupWasmStorage(fn *Function, name string) error {
	WASM_IMAGES_ROOT := "/mnt/faasedge/images"
	image_path := WASM_IMAGES_ROOT + "/" + fn.image
	rootfs_path := image_path + "/replicas/" + name
	/* Check if unique dir for replica exists; if so, remove */
	if _, err := os.Stat(rootfs_path); !os.IsNotExist(err) {
		os.RemoveAll(rootfs_path)
	}
	/* Create rootfs dir for Function instance */
	err := os.Mkdir(rootfs_path, 0744)
	if err != nil {
		log.Printf("[setupWasmStorage] Unable to create rootfs for WASM container '%s'", name)
		return err
	}

	/* Reflink all files in image rootfs */
	for _, file := range fn.imageFiles {
		err := reflink.Auto(image_path+"/rootfs/"+file, rootfs_path+"/"+file)
		if err != nil {
			log.Printf("[setupWasmStorage] Error creating reflink to file '%s' for container '%s'", file, name)
			return err
		}
	}
	return nil
}

func createWasmReplica(fn *Function, setActive bool) error {
	WASM_IMAGES_ROOT := "/mnt/faasedge/images"
	/* Generate UUID */
	name := fn.name + "-" + uuid.New().String()

	/* Create unique dir for replica and setup reflinks */
	err := setupWasmStorage(fn, name)
	if err != nil {
		return err
	}
	/* Exec wasmedge to create container process, retrieve PID */
	container_wasm_file := WASM_IMAGES_ROOT + "/" + fn.image + "/" + "function.wasm"
	container_dir_arg := ".:" + WASM_IMAGES_ROOT + "/replicas/" + name
	cmd := exec.Command("wasmedge", "--dir", container_dir_arg, container_wasm_file)

	cmd.Env = append(cmd.Environ(), "WASMEDGE_PLUGIN_PATH=/wasmedge/")
	cmd.Env = append(cmd.Environ(), "LD_LIBRARY_PATH=\"/lib/:/wasmedge/\"")

	err = cmd.Start()

	if err != nil {
		log.Printf("[createWasmReplica] Error starting WASM container '%s'", name)
	} else {
		pid := cmd.Process.Pid
		log.Printf("[createWasmReplica] WASM container '%s' started (PID = %d)", name, pid)
		/* Setup CNI for PID */
		ip, err := cninetwork.GetIPAddress(name, uint32(pid))
		if err != nil {
			return fmt.Errorf("[createWasmReplica] Unable to get IP for container '%s': %w", name, err)
		}
		// Store UUID,PID in fn
		if setActive {
			fn.activeReplicas[name] = ip
		} else {
			fn.idleReplicas[name] = ip
		}
		fn.pid[name] = uint32(pid)
	}

	return nil
}
