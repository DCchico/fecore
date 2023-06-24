#define _GNU_SOURCE

#include <wasmedge/wasmedge.h>
#include <stdio.h>
#include <errno.h>
#include <error.h>
#include <stdlib.h>
#include <signal.h>
#include <sched.h>

#include <linux/limits.h>
#include <fcntl.h>
#include <unistd.h>

#include <time.h>
#include <sys/socket.h>
#include <netdb.h>
#include <ifaddrs.h>

#define LIKELY(x) __builtin_expect ((x), 1)
#define UNLIKELY(x) __builtin_expect ((x), 0)

WasmEdge_VMContext *VMCxt;
WasmEdge_ConfigureContext *ConfCxt;
WasmEdge_Async *Async;
const char *function_name;

int main(int argc, const char* argv[]) {
  uint32_t argn = 0;
  uint32_t envn = 0;

  extern char **environ;

  function_name = argv[1];
  const char *dirs[2] = { argv[2] };
  int ns_num = atoi(argv[3]);

/* Begin join existing network namespace */
  char path[PATH_MAX];
  snprintf(path, sizeof(path), "/var/run/netns/fe_wasm_netns%d", ns_num);
  int net_ns = open(path, O_RDONLY);
  int status = setns(net_ns, 0);
  if(status != 0) {
    printf("ERROR joining NET NS /var/run/netns/fe_wasm_netns%d\n", ns_num);
    exit(-1);
  }
  else {
    printf("Joined NET NS\n");
    close(net_ns);
  }
/* End join network namespace */

/* Begin load WasmEdge deps */
  WasmEdge_ModuleInstanceContext *wasi_module;
  WasmEdge_ModuleInstanceContext *wasinn_module;

  WasmEdge_PluginLoadWithDefaultPaths();
  ConfCxt = WasmEdge_ConfigureCreate();
  if (UNLIKELY (ConfCxt == NULL)) {
    error (EXIT_FAILURE, 0, "could not create wasmedge configure");
  }

  WasmEdge_ConfigureAddHostRegistration(ConfCxt, WasmEdge_HostRegistration_Wasi);
  WasmEdge_ConfigureAddHostRegistration(ConfCxt, WasmEdge_HostRegistration_WasiNN);

  VMCxt = WasmEdge_VMCreate(ConfCxt, NULL);
  if (UNLIKELY(VMCxt == NULL)) {
    WasmEdge_ConfigureDelete(ConfCxt);
    error (EXIT_FAILURE, 0, "could not create wasmedge vm");
  }

  wasi_module = WasmEdge_VMGetImportModuleContext (VMCxt, WasmEdge_HostRegistration_Wasi);
  if (UNLIKELY (wasi_module == NULL)) {
    WasmEdge_VMDelete(VMCxt);
    WasmEdge_ConfigureDelete(ConfCxt);
    error (EXIT_FAILURE, 0, "could not get wasmedge wasi module context");
  }

  wasinn_module = WasmEdge_VMGetImportModuleContext (VMCxt, WasmEdge_HostRegistration_WasiNN);
  if (UNLIKELY (wasinn_module == NULL)) {
    WasmEdge_VMDelete(VMCxt);
    WasmEdge_ConfigureDelete(ConfCxt);
    error (EXIT_FAILURE, 0, "could not get wasmedge wasinn module context");
  }

  WasmEdge_ModuleInstanceInitWASI(wasi_module, NULL, argn, NULL, envn, dirs, 1);
/* End load WasmEdge deps */

  // Async = WasmEdge_VMAsyncRunWasmFromFile(VMCxt, function_name, WasmEdge_StringCreateByCString("_start"), NULL, 0);
  // WasmEdge_AsyncWait(Async);
  // WasmEdge_AsyncCancel(Async);
  // WasmEdge_AsyncDelete(Async);
  // /* Resources deallocations. */
  // WasmEdge_VMDelete(VMCxt);
  // WasmEdge_ConfigureDelete(ConfCxt);
  // WasmEdge_StringDelete(WasmEdge_StringCreateByCString("_start"));
  // exit(0);

/* Begin start web server */

/* End start web server */
  WasmEdge_Result Res = WasmEdge_VMRunWasmFromFile(VMCxt, argv[1], WasmEdge_StringCreateByCString("_start"), NULL, 0, NULL, 0);

  //while(1) {}

  if (WasmEdge_ResultOK(Res)) {
    printf("Success: %s\n", WasmEdge_ResultGetMessage(Res));
  } else {
    printf("Error message: %s\n", WasmEdge_ResultGetMessage(Res));
  }

  clock_t start, end;
  double elapsed;
  start = clock();

  end = clock();
  elapsed = ((double) (end - start)) / CLOCKS_PER_SEC;
  printf("Took %f sec to call WasmEdge_VMAsyncRunWasmFromFile()\n", elapsed);

  return 0;
}
