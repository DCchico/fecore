/* Web server based on sample code found here: https://bruinsslot.jp/post/simple-http-webserver-in-c/ */

#define _GNU_SOURCE

#include <stdio.h>
#include <errno.h>
#include <error.h>
#include <stdlib.h>
#include <signal.h>
#include <sched.h>
#include <sys/wait.h>

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

const char *function_name;
char func_output[4096];

int run_function(const char* function_name) {
  int comm[2];
  if ( pipe(comm) == -1 ) return -1;
  printf("run_function()\n");
  char *const argv[0];
  char *const envp[0];
  pid_t cpid = fork();
  if (cpid == -1) {
    printf("Could not fork()\n");
    return -1;
  } else if (cpid > 0) {
    int status;
    close(comm[1]);
    int outputsz = read(comm[0], func_output, sizeof(func_output));
    //printf("Function returned: %s (size=%d)\n", func_output, outputsz);
    //waitpid(cpid, &status, 0);
    wait(NULL);
    return outputsz;
  } else {
    dup2(comm[1], STDOUT_FILENO);
    close(comm[0]);
    close(comm[1]);
    execl("native-inf-rs", "165", (char *)0);
    _exit(EXIT_FAILURE);
  }
}

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

/* **** BEGIN RUN THE FUNCTION **** */

    int outputsz = run_function("native-inf-rs");

/* **** END RUN THE FUNCTION **** */

    //char resp[] = "HTTP/1.1 200 OK\r\nServer: runw\r\nContent-type: text/plain\r\n\r\nUnknown\r\n";
    char resp[] = "HTTP/1.1 200 OK\r\nServer: runw\r\nContent-type: text/plain\r\n\r\n";
    printf("function_output = %s", func_output);

    strcat(resp, func_output);
    strcat(resp, "\r\n");

    printf("resp = %s", resp);

  // Write to the socket
  int valwrite = write(newsockfd, resp, strlen(resp));
  if (valwrite < 0) {
      perror("webserver (write)");
      continue;
  }
  close(newsockfd);
  }
/* End start web server */

  return 0;
}
