package handlers

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"
	"github.com/docker/distribution/reference"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/openfaas/faas-provider/types"
	"github.com/pkg/errors"
	cninetwork "github.gatech.edu/faasedge/fecore/pkg/cninetwork"
	"github.gatech.edu/faasedge/fecore/pkg/service"
	"github.gatech.edu/faasedge/fecore/pkg/timec"
	"k8s.io/apimachinery/pkg/api/resource"
)

const annotationLabelPrefix = "com.openfaas.annotations."

// MakeDeployHandler returns a handler to deploy a function
func MakeDeployHandler(client *containerd.Client, cni gocni.CNI, secretMountPath string, alwaysPull bool, fs *FunctionStore) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Deploy] request: %s\n", string(body))

		req := types.FunctionDeployment{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Deploy] - error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		namespace := getRequestNamespace(req.Namespace)

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(client.NamespaceService(), namespace)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		namespaceSecretMountPath := getNamespaceSecretMountPath(secretMountPath, namespace)
		err = validateSecrets(namespaceSecretMountPath, req.Secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		name := req.Service
		ctx := namespaces.WithNamespace(context.Background(), namespace)

		fn := Function{}
		fn.name = name
		fn.namespace = namespace
		fn.pid = make(map[string]uint32)
		fn.imageFiles = make([]string, 0)
		fn.secretsPath = secretMountPath
		fn.activeReplicas = make(map[string]string)
		fn.idleReplicas = make(map[string]string)
		fn.idleReplicasTs = make(map[string]time.Time)
		// State management
		// fn.UserToFunctionReplica = make(map[string]map[string]fecore.StickinessRecord)

		deployErr := deploy(ctx, req, client, cni, namespaceSecretMountPath, alwaysPull, &fn, fs)
		if deployErr != nil {
			log.Printf("[Deploy] error deploying %s, error: %s\n", name, deployErr)
			http.Error(w, deployErr.Error(), http.StatusBadRequest)
			return
		}
		fn.replicas = 1
		err = fs.AddDeployedFunction(&fn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
}

// prepull is an optimization which means an image can be pulled before a deployment
// request, since a deployment request first deletes the active function before
// trying to deploy a new one.
func prepull(ctx context.Context, req types.FunctionDeployment, client *containerd.Client, alwaysPull bool) (containerd.Image, error) {
	start := time.Now()
	r, err := reference.ParseNormalizedNamed(req.Image)
	if err != nil {
		return nil, err
	}

	imgRef := reference.TagNameOnly(r).String()

	snapshotter := ""
	if val, ok := os.LookupEnv("snapshotter"); ok {
		snapshotter = val
	}

	image, err := service.PrepareImage(ctx, client, imgRef, "prepull", snapshotter, alwaysPull)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to pull image %s", imgRef)
	}

	size, _ := image.Size(ctx)
	log.Printf("Image for: %s size: %d, took: %fs\n", image.Name(), size, time.Since(start).Seconds())

	return image, nil
}

func deploy(ctx context.Context, req types.FunctionDeployment, client *containerd.Client, cni gocni.CNI,
	secretMountPath string, alwaysPull bool, fn *Function, fs *FunctionStore) error {

	// Get the host machine's IP address
	hostIP, err := getHostIP()
	if err != nil {
		return fmt.Errorf("failed to get host IP: %w", err)
	}

	// Add the Redis address as an environment variable
	if req.EnvVars == nil {
		req.EnvVars = make(map[string]string)
	}
	req.EnvVars["FECORE_REDIS_ADDRESS"] = fmt.Sprintf("%s:6379", hostIP)

	labels, err := buildLabels(&req)
	if err != nil {
		return fmt.Errorf("[deploy] Unable to build labels for Function '%s': %w", fn.name, err)
	}

	// TODO use container image to extra wasm data
	// Currenly hack is to specify the tar path as image url
	if val, ok := labels["module.wasm.image/variant"]; ok && (val == "compat-smart" || val == "compat") {
		WASM_IMAGES_ROOT := "/mnt/faasedge/images"
		r, err := os.Open(WASM_IMAGES_ROOT + "/" + req.Image + ".tgz")
		if err != nil {
			return err
		}
		err = untar(WASM_IMAGES_ROOT, r)
		if err != nil {
			return err
		}
		fn.image = WASM_IMAGES_ROOT + "/" + fn.name
		files, _ := os.ReadDir(fn.image + "/rootfs")
		for _, file := range files {
			fn.imageFiles = append(fn.imageFiles, file.Name())
		}
	} else {
		image, err := prepull(ctx, req, client, alwaysPull)
		if err != nil {
			return err
		}
		fn.image = image.Name()
	}

	fn.envProcess = req.EnvProcess
	fn.envVars = req.EnvVars
	fn.labels = labels

	if req.Limits != nil {
		if len(req.Limits.Memory) > 0 {
			qty, err := resource.ParseQuantity(req.Limits.Memory)
			if err != nil {
				fn.memoryLimit = 50 * 1024 * 1024
				log.Printf("[deploy] Error parsing memory limit '%q' as quantity for Function '%s': %s", req.Limits.Memory, fn.name, err.Error())
			} else {
				fn.memoryLimit = qty.Value()
				log.Printf("[deploy] Set memory limit for Function '%s' to %d bytes", fn.name, fn.memoryLimit)
			}
		} else {
			fn.memoryLimit = 50 * 1024 * 1024
			log.Printf("[deploy] No valid memory limit set for Function '%s', using default limit of %d bytes", fn.name, fn.memoryLimit)
		}

		if len(req.Limits.CPU) > 0 {
			cpuLimit, err := resource.ParseQuantity(req.Limits.CPU)
			if err != nil {
				fn.cpuLimit = 500 // Default to 500 milliCPU (0.5 CPU)
				log.Printf("[deploy] Error parsing CPU limit '%q' for Function '%s': %s", req.Limits.CPU, fn.name, err.Error())
			} else {
				fn.cpuLimit = cpuLimit.MilliValue()
				log.Printf("[deploy] Set CPU limit for Function '%s' to %d milliCPU", fn.name, fn.cpuLimit)
			}
		} else {
			fn.cpuLimit = 500 // Default to 500 milliCPU (0.5 CPU)
			log.Printf("[deploy] No valid CPU limit set for Function '%s', using default limit of %d milliCPU", fn.name, fn.cpuLimit)
		}
	} else {
		fn.memoryLimit = 50 * 1024 * 1024
		fn.cpuLimit = 500
		log.Printf("[deploy] No limits set for Function '%s', using default memory limit of %d bytes and CPU limit of %d milliCPU", fn.name, fn.memoryLimit, fn.cpuLimit)
	}

	startTime := time.Now()
	replicaName, createReplicaStatus := createReplica(client, cni, fs, fn, "Deploy", false)
	if createReplicaStatus == nil {
		coldStartTime := time.Since(startTime)
		fs.RecordColdStartTime(coldStartTime, "Deploy <"+fn.name+">")
		log.Printf("[deploy] Created container '%s' for Function '%s'", replicaName, fn.name)
	} else {
		log.Printf("[deploy] Failed to create container for Function '%s'", fn.name)
	}
	return createReplicaStatus
}

func buildLabels(request *types.FunctionDeployment) (map[string]string, error) {
	// Adapted from faas-swarm/handlers/deploy.go:buildLabels
	labels := map[string]string{}

	if request.Labels != nil {
		for k, v := range *request.Labels {
			labels[k] = v
		}
	}

	if request.Annotations != nil {
		for k, v := range *request.Annotations {
			key := fmt.Sprintf("%s%s", annotationLabelPrefix, k)
			if _, ok := labels[key]; !ok {
				labels[key] = v
			} else {
				return nil, errors.New(fmt.Sprintf("Key %s cannot be used as a label due to a conflict with annotation prefix %s", k, annotationLabelPrefix))
			}
		}
	}

	return labels, nil
}

func createTask(ctx context.Context, container containerd.Container, requestID string, cni gocni.CNI) (string, error) {
	defer timec.RecordDuration("(deploy.go) createTask() <requestID="+requestID+">", time.Now())

	name := container.ID()

	task, taskErr := container.NewTask(ctx, cio.BinaryIO("/usr/local/bin/fecore", nil))

	if taskErr != nil {
		return "", fmt.Errorf("unable to start task: %s, error: %w", name, taskErr)
	}

	log.Printf("Container ID: %s\tTask ID %s:\tTask PID: %d\t\n", name, task.ID(), task.Pid())

	labels := map[string]string{}
	result, err := cninetwork.CreateCNINetwork(ctx, cni, task, labels)

	if err != nil {
		return "", err
	}

	// ip, err := cninetwork.GetIPAddress(name, task.Pid())
	// if err != nil {
	// 	return err
	// }
	ip := result.Interfaces["eth1"].IPConfigs[0].IP.String()

	log.Printf("%s has IP: %s.\n", name, ip)

	_, waitErr := task.Wait(ctx)
	if waitErr != nil {
		return "", errors.Wrapf(waitErr, "Unable to wait for task to start: %s", name)
	}

	if startErr := task.Start(ctx); startErr != nil {
		return "", errors.Wrapf(startErr, "Unable to start task: %s", name)
	}
	return ip, nil
}

func prepareEnv(envProcess string, reqEnvVars map[string]string) []string {
	envs := []string{}
	fprocessFound := false
	fprocess := "fprocess=" + envProcess
	if len(envProcess) > 0 {
		fprocessFound = true
	}

	for k, v := range reqEnvVars {
		if k == "fprocess" {
			fprocessFound = true
			fprocess = v
		} else {
			envs = append(envs, k+"="+v)
		}
	}
	if fprocessFound {
		envs = append(envs, fprocess)
	}
	return envs
}

// getOSMounts provides a mount for os-specific files such
// as the hosts file and resolv.conf
func getOSMounts() []specs.Mount {
	// Prior to hosts_dir env-var, this value was set to
	// os.Getwd()
	hostsDir := "/var/lib/fecore"
	if v, ok := os.LookupEnv("hosts_dir"); ok && len(v) > 0 {
		hostsDir = v
	}

	mounts := []specs.Mount{}
	mounts = append(mounts, specs.Mount{
		Destination: "/etc/resolv.conf",
		Type:        "bind",
		Source:      path.Join(hostsDir, "resolv.conf"),
		Options:     []string{"rbind", "ro"},
	})

	mounts = append(mounts, specs.Mount{
		Destination: "/etc/hosts",
		Type:        "bind",
		Source:      path.Join(hostsDir, "hosts"),
		Options:     []string{"rbind", "ro"},
	})
	return mounts
}

func validateSecrets(secretMountPath string, secrets []string) error {
	for _, secret := range secrets {
		if _, err := os.Stat(path.Join(secretMountPath, secret)); err != nil {
			return fmt.Errorf("unable to find secret: %s", secret)
		}
	}
	return nil
}

// Ref: https://medium.com/@skdomino/taring-untaring-files-in-go-6b07cf56bc07
// Untar takes a destination path and a reader; a tar reader loops over the tarfile
// creating the file structure at 'dst' along the way, and writing any files
func untar(dst string, r io.Reader) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		switch {
		// if no more files are found return
		case err == io.EOF:
			return nil
		// return any other error
		case err != nil:
			return err
		// if the header is nil, just skip it (not sure how this happens)
		case header == nil:
			continue
		}
		// the target location where the dir/file should be created
		target := filepath.Join(dst, header.Name)
		// the following switch could also be done using fi.Mode(), not sure if there
		// a benefit of using one vs. the other.
		// fi := header.FileInfo()
		// check the file type
		switch header.Typeflag {
		// if its a dir and it doesn't exist create it
		case tar.TypeDir:
			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					return err
				}
			}
		// if it's a file create it
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			// copy over contents
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			// manually close here after each file operation; defering would cause each file close
			// to wait until all operations have completed.
			f.Close()
		}
	}
}

// getHostIP returns the IP address of the host machine
func getHostIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no suitable IP address found")
}
