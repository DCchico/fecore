# DOES NOT WORK

## Install & Build

```
curl -sLO https://github.com/second-state/WasmEdge-native-infamples/raw/master/tflite-birds_v1-image/lite-model_aiy_vision_classifier_birds_V1_3.tflite
curl -sLO https://github.com/second-state/WasmEdge-native-infamples/raw/master/tflite-birds_v1-image/birdx224x224x3.rgb
```


```
sudo apt-get install libboost-all-dev
```

```
g++ -o main main.cpp -lpthread -lboost_system -ltensorflow
```

## Run

```

```


### Buildah + Podman 
```
buildah bud --annotation "module.wasm.image/variant=compat" -t native-inf .
podman run --network=host --runtime=/usr/local/bin/crun native-inf:latest
```

### Buildah + Ctr 

```
sudo docker login // to login to docker
sudo buildah build --annotation "module.wasm.image/variant=compat-smart" -t native-inf .
sudo buildah push --authfile ~/.docker/config.json localhost/native-inf:latest docker://<docker-username>/native-inf:latest
sudo ctr i pull docker.io/<docker-username>/native-inf:latest
sudo ctr run -d --net-host --label module.wasm.image/variant=compat-smart docker.io/<docker-username>/native-inf:latest native-inf
```

```
curl http://127.0.0.1:8080
```

## Running with Fecore

Assuming its pushed to `docker.io/<docker-username>/native-inf:latest` and the services are up (`fecure up` & `fecore provider`)

```
faas-cli deploy --image docker.io/<docker-username>/native-inf:latest --name native-inf --label module.wasm.image/variant=compat-smart
```

```
curl http://10.62.0.1:8081/function/native-inf
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
- https://chat.openai.com/