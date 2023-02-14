# FaaSEdge Core

Core is the execution engine for FaaSEdge. It is responsible for managing the lifecycle of process-based containers and WASM containers that run serverless functions. It is based on the [faasd provider](https://github.com/openfaas/faasd) from the [OpenFaaS](https://github.com/openfaas) project.

Similar to faasd, it relies on [containerd](https://containerd.io/) and [Container Networking Interface (CNI)](https://github.com/containernetworking/cni) for low-level container orchestration and management, but extends the original functionality to include support for different container types (e.g., WASM) and more robust function management policies (e.g., separate containers for each function invocation).

![FaaSEdge Node](docs/media/FaaSEdge-Node.png)
