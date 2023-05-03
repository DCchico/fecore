## Build

```
curl -sLO https://github.com/second-state/WasmEdge-WASINN-examples/raw/master/tflite-birds_v1-image/lite-model_aiy_vision_classifier_birds_V1_3.tflite
curl -sLO https://github.com/second-state/WasmEdge-WASINN-examples/raw/master/tflite-birds_v1-image/bird.jpg
```

```
cargo build --target wasm32-wasi
```

## Run

### Wasmedge
```
cd target/wasm32-wasi/debug
wasmedge --dir .:. wasinn-ex.wasm
```

### Buildah + Podman 
```
buildah bud --annotation "module.wasm.image/variant=compat" -t wasinn-ex .

podman run --network=host --runtime=/usr/local/bin/crun -e WASMEDGE_PLUGIN_PATH="/wasmedge/"  -e LD_LIBRARY_PATH="/lib/:/wasmedge/" wasinn-ex:latest
```

### Buildah + Ctr 

```
sudo docker login // to login to docker
sudo buildah build --annotation "module.wasm.image/variant=compat-smart" -t wasinn-ex .
sudo buildah push --authfile ~/.docker/config.json localhost/wasinn-ex:latest docker://<docker-username>/wasinn-ex:latest
sudo ctr i pull docker.io/<docker-username>/wasinn-ex:latest
sudo ctr run -d --net-host --label module.wasm.image/variant=compat-smart --env WASMEDGE_PLUGIN_PATH=/wasmedge/ --env LD_LIBRARY_PATH="/lib/:/wasmedge/"  docker.io/<docker-username>/wasinn-ex:latest wasinn-ex
```

```
curl http://127.0.0.1:8080 -H "tensor: birdx224x224x3.rgb"
```

## Running with Fecore

Assuming its pushed to `docker.io/<docker-username>/wasinn-ex:latest` and the services are up (`fecure up` & `fecore provider`)

```
faas-cli deploy --image docker.io/<docker-username>/wasinn-ex:latest --name wasinn-ex --label module.wasm.image/variant=compat-smart
```

```
curl http://10.62.0.1:8081/function/wasinn-ex -H "tensor: birdx224x224x3.rgb"
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
- https://wasmedge.org/book/en/write_wasm/rust/wasinn.html#get-wasmedge-with-wasi-nn-plug-in-tensorflow-lite-backend