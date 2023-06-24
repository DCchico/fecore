# runw

This dir contains source files for the *runw* utility that acts as a proxy and loader for WebAssembly binaries.

There are two versions:

1. `runw_web.c` has a built-in webserver that listens on port 8080 and invokes the WASM code when a client connects.
2. `runw_standalone.c` is essentially the same but lacks a webserver component, instead relying on the WASM code to run the webserver.

Both versions drop into a pre-created network namespace before executing the WASM code.

## TODO

1. Make the webserver in `runw_web.c` more robust
- Have it check for GET/POST requests and act accordingly
- Handle headers/inputs sent via HTTP and pass these to the WASM code when running it
- Retrieve stdout from the WASM code and return the value to caller via HTTP
