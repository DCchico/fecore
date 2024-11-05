package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/KarpelesLab/reflink"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	gocni "github.com/containerd/go-cni"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/openfaas/faas-provider/types"
	"github.gatech.edu/faasedge/fecore/pkg/service"

	"github.gatech.edu/faasedge/fecore/pkg/timec"
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
		if f, err := fs.GetDeployedFunction(functionName, "ReplicaHandler"); err == nil {
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

func createReplica(client *containerd.Client, cni gocni.CNI, fs *FunctionStore, fn *Function, requestID string, setActive bool) (string, error) {
	defer timec.RecordDuration("(replicas.go).createReplica <requestID="+requestID+">", time.Now())
	ctx := namespaces.WithNamespace(context.Background(), fn.namespace)

	if val, ok := fn.labels["module.wasm.image/variant"]; ok && (val == "compat-smart" || val == "compat") {
		wasmName, ip, wasmCmd, err := createWasmReplica(fn, fs, requestID, setActive)
		wasmPid := wasmCmd.Process.Pid
		if err == nil {
			/* Setup CNI for PID */
			//cniResult, err := cninetwork.CreateWasmCNINetwork(ctx, cni, wasmName, wasmPid, fn.labels)
			// labels := map[string]string{}
			// result, err := cninetwork.CreateWasmCNINetwork(ctx, cni, wasmName, wasmPid, requestID, labels)
			// if err != nil {
			// 	log.Printf("[createReplica] Could not setup networking for WASM container '%s'", wasmName)
			// 	return "", err
			// }
			// log.Printf("IP for '%s' = %s", wasmName, result.Interfaces["eth1"].IPConfigs[0].IP)
			// // Store UUID,PID in fn
			// ip := result.Interfaces["eth1"].IPConfigs[0].IP.String()
			// wasmCmd.Process.Signal(syscall.SIGUSR1)
			fs.dfMu.Lock()
			if setActive {
				fn.activeReplicas[wasmName] = ip
			} else {
				fn.idleReplicas[wasmName] = ip
			}
			fn.pid[wasmName] = uint32(wasmPid)
			fs.dfMu.Unlock()
		}
		return wasmName, nil
	}

	// snapshotter := "overlay"
	snapshotter := ""
	if val, ok := os.LookupEnv("snapshotter"); ok {
		snapshotter = val
	}

	image, err := service.PrepareImage(ctx, client, fn.image, requestID, snapshotter, false)
	if err != nil {
		return "", fmt.Errorf("[createReplica] Unable to pull image %s, %w", fn.image, err)
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

	// // if wasm container, we need to mount some paths and set env variables for wasmedge to load WASI-NN related plugins
	// if val, ok := labels["module.wasm.image/variant"]; ok && (val == "compat-smart" || val == "compat") {
	// 	mounts = append(mounts, specs.Mount{
	// 		Destination: path.Join("/lib/"),
	// 		Type:        "bind",
	// 		Source:      path.Join("/usr/local/lib/"),
	// 		Options:     []string{"rbind", "ro"},
	// 	})
	// 	mounts = append(mounts, specs.Mount{
	// 		Destination: path.Join("/wasmedge/"),
	// 		Type:        "bind",
	// 		Source:      path.Join("/usr/local/lib/wasmedge/"),
	// 		Options:     []string{"rbind", "ro"},
	// 	})

	// 	envs = append(envs, "WASMEDGE_PLUGIN_PATH=/wasmedge/")
	// 	envs = append(envs, "LD_LIBRARY_PATH=\"/lib/:/wasmedge/\"")
	// }

	var memory *specs.LinuxMemory
	var cpu *specs.LinuxCPU

	if fn.memoryLimit > 0 {
		memory = &specs.LinuxMemory{
			Limit: &fn.memoryLimit,
		}
	}

	if fn.cpuLimit > 0 {
		period := uint64(100000)
		quota := fn.cpuLimit * int64(period/1000) // Convert milliCPU to quota
		cpu = &specs.LinuxCPU{
			Period: &period,
			Quota:  &quota,
		}
	}

	log.Printf("[createReplica] Creating new container for Function '%s' with memory limit: %d bytes, CPU limit: %d milliCPU", name, fn.memoryLimit, fn.cpuLimit)
	container, err := client.NewContainer(
		ctx,
		name,
		requestID,
		containerd.WithImage(image),
		containerd.WithSnapshotter(snapshotter),
		containerd.WithNewSnapshot(name+"-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image),
			oci.WithHostname(name),
			oci.WithCapabilities([]string{"CAP_NET_RAW"}),
			oci.WithMounts(mounts),
			oci.WithAnnotations(labels),
			oci.WithEnv(envs),
			withMemory(memory),
			withCPU(cpu)),
		containerd.WithContainerLabels(labels),
	)

	if err != nil {
		return "", fmt.Errorf("[createReplica] Unable to create container '%s': %w", name, err)
	}

	ip, createTaskStatus := createTask(ctx, container, requestID, cni)
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
		// ip, err := cninetwork.GetIPAddress(name, task.Pid())
		// if err != nil {
		// 	return "", fmt.Errorf("[createReplica] Unable to get IP for container '%s': %w", name, err)
		// }
		fs.dfMu.Lock()
		if setActive {
			fn.activeReplicas[name] = ip
		} else {
			fn.idleReplicas[name] = ip
		}
		fs.dfMu.Unlock()
		return name, nil
	}
	return "", err
}

func setupWasmStorage(fn *Function, name string, requestID string) error {
	defer timec.RecordDuration("(replicas.go).setupWasmStorage <requestID="+requestID+">", time.Now())
	// WASM_IMAGES_ROOT := "/mnt/faasedge/images"
	image_path := fn.image
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

func createWasmReplica(fn *Function, fs *FunctionStore, requestID string, setActive bool) (string, string, *exec.Cmd, error) {
	defer timec.RecordDuration("(replicas.go).createWasmReplica <requestID="+requestID+">", time.Now())
	// WASM_IMAGES_ROOT := "/mnt/faasedge/images"
	/* Generate UUID */
	replicaName := fn.name + "-" + uuid.New().String()

	/* Create unique dir for replica and setup reflinks */
	err := setupWasmStorage(fn, replicaName, requestID)
	if err != nil {
		return "", "", nil, err
	}
	/* Get the next available network namespace */
	netnsNum, IP := fs.GetNetNS()
	/* Exec wasmedge to create container process, retrieve PID */
	container_wasm_file := fn.image + "/" + "function.wasm"
	container_dir_arg := ".:" + fn.image + "/replicas/" + replicaName
	log.Printf("[createWasmReplica] Creating replica with command: /mnt/faasedge/runw %s %s %s", container_wasm_file, container_dir_arg, strconv.FormatInt(netnsNum, 10))
	cmd := exec.Command("/mnt/faasedge/runw", container_wasm_file, container_dir_arg, strconv.FormatInt(netnsNum, 10))

	// cmd.Env = append(cmd.Environ(), "WASMEDGE_PLUGIN_PATH=/wasmedge/")
	// cmd.Env = append(cmd.Environ(), "LD_LIBRARY_PATH=\"/lib/:/wasmedge/\"")

	err = cmd.Start()

	if err == nil {
		log.Printf("[createWasmReplica] Created WASM container '%s' with IP '%s'", replicaName, IP)

		return replicaName, IP, cmd, nil
	}

	/* TODO: If error starting process, return netnsNum/IP back to pool as available */

	log.Printf("[createWasmReplica] Error starting WASM container '%s'", replicaName)
	return "", "", nil, err
}

// ReplicaUpdate represents the payload for updating a single function's replica count.
type ReplicaUpdate struct {
	FunctionName string `json:"function_name"`
	ReplicaCount int    `json:"replica_count"`
}

// sendReplicaUpdate sends the replica count for a specific function to stateflow.
func sendReplicaUpdate(fecoreIP string, fecorePort int, stateflowIP string, stateflowPort string, functionName string, replicaCount int) {
	stateflowAddr := fmt.Sprintf("http://%s:%s/replica", stateflowIP, stateflowPort)

	updatePayload := ReplicaUpdate{
		FunctionName: functionName,
		ReplicaCount: replicaCount,
	}

	jsonPayload, err := json.Marshal(updatePayload)
	if err != nil {
		log.Printf("[sendReplicaUpdate] Error marshaling JSON: %v", err)
		return
	}

	req, err := http.NewRequest("POST", stateflowAddr, bytes.NewBuffer(jsonPayload))
	if err != nil {
		log.Printf("[sendReplicaUpdate] Error creating request: %v", err)
		return
	}

	req.Header.Set("Fecore-IP", fecoreIP)
	req.Header.Set("Fecore-Port", strconv.Itoa(fecorePort))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[sendReplicaUpdate] Error sending request to stateflow: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[sendReplicaUpdate] Failed to update stateflow, status code: %d", resp.StatusCode)
	} else {
		log.Printf("[sendReplicaUpdate] Successfully updated replica count for function %s to stateflow", functionName)
	}
}

func sendReplicaUpdateAsync(fecoreIP string, fecorePort int, stateflowIP string, stateflowPort string, functionName string, replicaCount int) {
	go func() {
		sendReplicaUpdate(fecoreIP, fecorePort, stateflowIP, stateflowPort, functionName, replicaCount)
	}()
}

// Add these helper functions at the end of the file
func withMemory(mem *specs.LinuxMemory) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, c *containers.Container, s *oci.Spec) error {
		if mem != nil {
			if s.Linux == nil {
				s.Linux = &specs.Linux{}
			}
			if s.Linux.Resources == nil {
				s.Linux.Resources = &specs.LinuxResources{}
			}
			if s.Linux.Resources.Memory == nil {
				s.Linux.Resources.Memory = &specs.LinuxMemory{}
			}
			s.Linux.Resources.Memory.Limit = mem.Limit
		}
		return nil
	}
}

func withCPU(cpu *specs.LinuxCPU) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, c *containers.Container, s *oci.Spec) error {
		if cpu != nil {
			if s.Linux == nil {
				s.Linux = &specs.Linux{}
			}
			if s.Linux.Resources == nil {
				s.Linux.Resources = &specs.LinuxResources{}
			}
			if s.Linux.Resources.CPU == nil {
				s.Linux.Resources.CPU = &specs.LinuxCPU{}
			}
			s.Linux.Resources.CPU = cpu
		}
		return nil
	}
}
