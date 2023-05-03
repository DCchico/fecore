## Build

```
curl -sLO https://github.com/second-state/WasmEdge-native-inf-rsamples/raw/master/tflite-birds_v1-image/lite-model_aiy_vision_classifier_birds_V1_3.tflite
curl -sLO https://github.com/second-state/WasmEdge-native-inf-rsamples/raw/master/tflite-birds_v1-image/birdx224x224x3.rgb
```

```
cargo build --target x86_64-unknown-linux-gnu
```

## Run

```
cd target/x86_64-unknown-linux-gnu/debug
./native-inf-rs
```

### Buildah + Podman 
```
buildah bud -t native-inf-rs .
podman run --network=host --runtime=/usr/local/bin/crun native-inf-rs:latest
```

```
curl -vk http://127.0.0.1:8080/ -H "tensor: birdx224x224x3.rgb"
```

### Buildah + Ctr 

```
sudo docker login // to login to docker
sudo buildah build -t native-inf-rs .
sudo buildah push --authfile ~/.docker/config.json localhost/native-inf-rs:latest docker://<docker-username>/native-inf-rs:latest
sudo ctr i pull docker.io/<docker-username>/native-inf-rs:latest
sudo ctr run -d --net-host --label module.wasm.image/variant=compat-smart docker.io/<docker-username>/native-inf-rs:latest native-inf-rs
```

## Running with Fecore

Assuming its pushed to `docker.io/<docker-username>/native-inf-rs:latest` and the services are up (`fecure up` & `fecore provider`)

```
faas-cli deploy --image docker.io/<docker-username>/native-inf-rs:latest --name native-inf-rs
```

```
curl http://10.62.0.1:8081/function/native-inf-rs -H "tensor: birdx224x224x3.rgb"
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
- https://github.com/Jonarod/tensorflow_lite_alpine/blob/master/example/Dockerfile
