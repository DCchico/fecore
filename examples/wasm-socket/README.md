## Build

```
cargo build --target wasm32-wasi
```

## Run

### Buildah + Podman 
```
buildah bud --annotation "module.wasm.image/variant=compat" -t wasm-sock .
podman run --network=host --runtime=/usr/local/bin/crun wasm-sock:latest
```

### Buildah + Ctr 

```
sudo docker login // to login to docker
sudo buildah build --annotation "module.wasm.image/variant=compat-smart" -t wasm-sock .
sudo buildah push --authfile ~/.docker/config.json localhost/wasm-sock:latest docker://<docker-username>/wasm-sock:latest
sudo ctr i pull docker.io/<docker-username>/wasm-sock:latest
sudo ctr run -d  --net-host  --label module.wasm.image/variant=compat-smart docker.io/<docker-username>/wasm-sock:latest wasm-sock
```

```
curl http://127.0.0.1:8080
```

## Running with Fecore

Assuming its pushed to `docker.io/<docker-username>/wasm-sock:latest` and the services are up (`fecure up` & `fecore provider`)


### THIS DOESN'T SEEM TO WORK 
```
faas-cli deploy --image docker.io/<docker-username>/wasm-sock:latest --name wasm-sock --label module.wasm.image/variant=compat-smart
```

```
curl http://10.62.0.1:8081/function/wasm-sock
```

## Troubleshoot

If the podman run doesn't stop
```
ps aux | grep podman
sudo kill -9 <pid of the process above>
podman ps
podman kill <cid>
podman rm <cid>
```

If the containerd task doesn't stop
```
sudo ctr t ls
sudo ctr t rm <tid> // doesn't work
```

```
ps aux | grep shim
sudo kill -9 <pid of the process corresponding to task shim>
sudo ctr c rm <cid>
```


## Reference
- https://github.com/second-state/wasmedge_wasi_socket/tree/main/examples/http_server