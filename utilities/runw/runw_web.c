/* Web server based on sample code found here: https://bruinsslot.jp/post/simple-http-webserver-in-c/ */

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
#include <netdb.h>
#include <ifaddrs.h>

#include <arpa/inet.h>
#include <string.h>
#include <sys/socket.h>

#define LIKELY(x) __builtin_expect ((x), 1)
#define UNLIKELY(x) __builtin_expect ((x), 0)

#define PORT 8080
#define BUFFER_SIZE 1024

WasmEdge_VMContext *VMCxt;
WasmEdge_ConfigureContext *ConfCxt;
WasmEdge_Async *Async;
const char *function_name;

int main(int argc, const char* argv[]) {
  clock_t start, end;
  double elapsed;

  uint32_t argn = 0;
  uint32_t envn = 0;

  extern char **environ;

  function_name = argv[1];
  const char *dirs[2] = { argv[2] };
  int ns_num = atoi(argv[3]);


  start = clock();
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
    end = clock();
    elapsed = ((double) (end - start)) / CLOCKS_PER_SEC;
    printf("Joined NET NS in %f seconds\n", elapsed);
    close(net_ns);
  }
/* End join existing network namespace */

/* Begin start web server */
  char buffer[BUFFER_SIZE];
  int sockfd = socket(AF_INET, SOCK_STREAM, 0);
  if (sockfd == -1) {
      perror("Unable to create socket for web server");
      return 1;
  }
  struct sockaddr_in host_addr;
  int host_addrlen = sizeof(host_addr);

  host_addr.sin_family = AF_INET;
  host_addr.sin_port = htons(PORT);
  host_addr.sin_addr.s_addr = htonl(INADDR_ANY);

  // Create client address
  struct sockaddr_in client_addr;
  int client_addrlen = sizeof(client_addr);

  if (bind(sockfd, (struct sockaddr *)&host_addr, host_addrlen) != 0) {
      perror("Unable to bind webserver to port");
      return 1;
  }

  if (listen(sockfd, SOMAXCONN) != 0) {
      perror("Webserver unable to listen on port");
      return 1;
  }

  WasmEdge_ModuleInstanceContext *wasi_module;
  WasmEdge_ModuleInstanceContext *wasinn_module;

  printf("=> WasmEdge_PluginLoadWithDefaultPaths()\n");
  WasmEdge_PluginLoadWithDefaultPaths();

/* Begin load WASM deps */

  printf("=> WasmEdge_ConfigureCreate()\n");
  ConfCxt = WasmEdge_ConfigureCreate();
  if (UNLIKELY (ConfCxt == NULL)) {
    error (EXIT_FAILURE, 0, "could not create wasmedge configure");
  }

  printf("=> WasmEdge_ConfigureAddHostRegistration(Wasi)\n");
  WasmEdge_ConfigureAddHostRegistration(ConfCxt, WasmEdge_HostRegistration_Wasi);
  printf("=> WasmEdge_ConfigureAddHostRegistration(WasiNN)\n");
  WasmEdge_ConfigureAddHostRegistration(ConfCxt, WasmEdge_HostRegistration_WasiNN);

  printf("=> WasmEdge_VMCreate()\n");
  VMCxt = WasmEdge_VMCreate(ConfCxt, NULL);
  if (UNLIKELY(VMCxt == NULL)) {
    WasmEdge_ConfigureDelete(ConfCxt);
    error (EXIT_FAILURE, 0, "could not create wasmedge vm");
  }

  printf("=> WasmEdge_VMGetImportModuleContext(Wasi)\n");
  wasi_module = WasmEdge_VMGetImportModuleContext (VMCxt, WasmEdge_HostRegistration_Wasi);
  if (UNLIKELY (wasi_module == NULL)) {
    WasmEdge_VMDelete(VMCxt);
    WasmEdge_ConfigureDelete(ConfCxt);
    error (EXIT_FAILURE, 0, "could not get wasmedge wasi module context");
  }

  printf("=> WasmEdge_VMGetImportModuleContext(WasiNN)\n");
  wasinn_module = WasmEdge_VMGetImportModuleContext (VMCxt, WasmEdge_HostRegistration_WasiNN);
  if (UNLIKELY (wasinn_module == NULL)) {
    WasmEdge_VMDelete(VMCxt);
    WasmEdge_ConfigureDelete(ConfCxt);
    error (EXIT_FAILURE, 0, "could not get wasmedge wasinn module context");
  }

  printf("=> WasmEdge_ModuleInstanceInitWASI()\n");
  WasmEdge_ModuleInstanceInitWASI(wasi_module, NULL, argn, NULL, envn, dirs, 1);
/* End load WASM deps */

  for (;;) {
    // Accept incoming connections
    int newsockfd = accept(sockfd, (struct sockaddr *)&host_addr,
                            (socklen_t *)&host_addrlen);
    if (newsockfd < 0) {
        perror("webserver (accept)");
        continue;
    }

    // Get client address
    int sockn = getsockname(newsockfd, (struct sockaddr *)&client_addr,
                            (socklen_t *)&client_addrlen);
    if (sockn < 0) {
        perror("webserver (getsockname)");
        continue;
    }

    // Read from the socket
    int valread = read(newsockfd, buffer, BUFFER_SIZE);
    if (valread < 0) {
        perror("webserver (read)");
        continue;
    }

/* Begin do WASM inference */
  printf("=> WasmEdge_VMRunWasmFromFile()\n");
  WasmEdge_Result Res = WasmEdge_VMRunWasmFromFile(VMCxt, argv[1], WasmEdge_StringCreateByCString("_start"), NULL, 0, NULL, 0);

  char resp[] = "HTTP/1.1 200 OK\r\nServer: runw\r\nContent-type: text/plain\r\n\r\nUnknown\r\n";
  if (WasmEdge_ResultOK(Res)) {
    printf("Success: %s\n", WasmEdge_ResultGetMessage(Res));
    sprintf(resp, "HTTP/1.1 200 OK\r\nServer: runw\r\nContent-type: text/plain\r\n\r\nSuccess\r\n");
  } else {
    printf("Error message: %s\n", WasmEdge_ResultGetMessage(Res));
    sprintf(resp, "HTTP/1.1 200 OK\r\nServer: runw\r\nContent-type: text/plain\r\n\r\nFailure\r\n");
  }



  // Write to the socket
  int valwrite = write(newsockfd, resp, strlen(resp));
  if (valwrite < 0) {
      perror("webserver (write)");
      continue;
  }
  close(newsockfd);
  //break;
/* End do WASM inference */
  }
/* End start web server */
  WasmEdge_StringDelete(WasmEdge_StringCreateByCString("_start"));
  WasmEdge_VMDelete(VMCxt);
  WasmEdge_ConfigureDelete(ConfCxt);
  return 0;
}
