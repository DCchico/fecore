#! /bin/bash


var=$(podman run -d --runtime=/usr/local/bin/crun --network=host native-inf-rs:latest)
tmp=$(curl http://127.0.0.1:8080 -H "tensor: birdx224x224x3.rgb")
echo $var