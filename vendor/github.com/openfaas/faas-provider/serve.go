// Copyright (c) Alex Ellis 2017. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package bootstrap

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	// "github.com/openfaas/faas-provider/auth"
	"github.com/openfaas/faas-provider/types"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NameExpression for a function / service
const NameExpression = "-a-zA-Z_0-9."

var r *mux.Router

// Mark this as a Golang "package"
func init() {
	r = mux.NewRouter()
}

// Router gives access to the underlying router for when new routes need to be added.
func Router() *mux.Router {
	return r
}

// Serve load your handlers into the correct OpenFaaS route spec. This function is blocking.
func Serve(handlers *types.FaaSHandlers, config *types.FaaSConfig) {

	// if config.EnableBasicAuth {
	//	reader := auth.ReadBasicAuthFromDisk{
	//		SecretMountPath: config.SecretMountPath,
	//	}

	//	credentials, err := reader.Read()
	//	if err != nil {
	//		log.Fatal(err)
	//	}

		// handlers.FunctionReader = auth.DecorateWithBasicAuth(handlers.FunctionReader, credentials)
		// handlers.DeployHandler = auth.DecorateWithBasicAuth(handlers.DeployHandler, credentials)
		// handlers.DeleteHandler = auth.DecorateWithBasicAuth(handlers.DeleteHandler, credentials)
		// handlers.UpdateHandler = auth.DecorateWithBasicAuth(handlers.UpdateHandler, credentials)
		// handlers.ReplicaReader = auth.DecorateWithBasicAuth(handlers.ReplicaReader, credentials)
		// handlers.ReplicaUpdater = auth.DecorateWithBasicAuth(handlers.ReplicaUpdater, credentials)
		// handlers.InfoHandler = auth.DecorateWithBasicAuth(handlers.InfoHandler, credentials)
		// handlers.SecretHandler = auth.DecorateWithBasicAuth(handlers.SecretHandler, credentials)
		// handlers.LogHandler = auth.DecorateWithBasicAuth(handlers.LogHandler, credentials)
	//}

	hm := newHttpMetrics()

	// System (auth) endpoints
	r.HandleFunc("/system/functions", hm.InstrumentHandler(handlers.FunctionReader, "")).Methods(http.MethodGet)
	r.HandleFunc("/system/functions", hm.InstrumentHandler(handlers.DeployHandler, "")).Methods(http.MethodPost)
	r.HandleFunc("/system/functions", hm.InstrumentHandler(handlers.DeleteHandler, "")).Methods(http.MethodDelete)
	r.HandleFunc("/system/functions", hm.InstrumentHandler(handlers.UpdateHandler, "")).Methods(http.MethodPut)

	r.HandleFunc("/system/function/{name:["+NameExpression+"]+}",
		hm.InstrumentHandler(handlers.ReplicaReader, "/system/function")).Methods(http.MethodGet)
	r.HandleFunc("/system/scale-function/{name:["+NameExpression+"]+}",

		hm.InstrumentHandler(handlers.ReplicaUpdater, "/system/scale-function")).Methods(http.MethodPost)
	r.HandleFunc("/system/info",
		hm.InstrumentHandler(handlers.InfoHandler, "")).Methods(http.MethodGet)

	r.HandleFunc("/system/secrets",
		hm.InstrumentHandler(handlers.SecretHandler, "")).Methods(http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete)
	r.HandleFunc("/system/logs",
		hm.InstrumentHandler(handlers.LogHandler, "")).Methods(http.MethodGet)

	r.HandleFunc("/system/namespaces", hm.InstrumentHandler(handlers.ListNamespaceHandler, "")).Methods(http.MethodGet)

	proxyHandler := handlers.FunctionProxy

	// Open endpoints
	r.HandleFunc("/function/{name:["+NameExpression+"]+}", proxyHandler)
	r.HandleFunc("/function/{name:["+NameExpression+"]+}/", proxyHandler)
	r.HandleFunc("/function/{name:["+NameExpression+"]+}/{params:.*}", proxyHandler)

	if handlers.HealthHandler != nil {
		r.HandleFunc("/healthz", handlers.HealthHandler).Methods(http.MethodGet)
	}

	r.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)

	readTimeout := config.ReadTimeout
	writeTimeout := config.WriteTimeout

	port := 8080
	if config.TCPPort != nil {
		port = *config.TCPPort
	}

	hosts := []string{"0.0.0.0"}
	if bind := os.Getenv("FECORE_BIND_ADDR"); bind != "" {
		hosts = []string{bind}
		if bind != "127.0.0.1" {
			hosts = append(hosts, "127.0.0.1")
		}
	}
	errCh := make(chan error, len(hosts))
	for _, host := range hosts {
		host := host
		go func() {
			addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
			ln, err := net.Listen("tcp4", addr)
			if err != nil {
				errCh <- fmt.Errorf("listen %s: %w", addr, err)
				return
			}
			log.Printf("HTTP listening on %s", ln.Addr())
			srv := &http.Server{
				Addr:           addr,
				ReadTimeout:    readTimeout,
				WriteTimeout:   writeTimeout,
				MaxHeaderBytes: http.DefaultMaxHeaderBytes,
				Handler:        r,
			}
			errCh <- srv.Serve(ln)
		}()
	}
	log.Fatal(<-errCh)
}
