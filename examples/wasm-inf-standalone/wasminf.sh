#! /bin/bash

wasmedge --dir .:/home/oprabhu3/go/src/github.com/faasedge/fecore/examples/wasm-inference/data /home/oprabhu3/go/src/github.com/faasedge/fecore/examples/wasm-inference/target/wasm32-wasi/debug/wasm-inference-aot.wasm&

curl --retry 20 --retry-delay 0 --max-time 0 --retry-all-errors http://127.0.0.1:28080 -H "tensor: birdx224x224x3.rgb"

kill -9 `pgrep wasmedge`