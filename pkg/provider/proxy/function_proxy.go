/*
Based on "github.com/openfaas/faas-provider/proxy"
*/
package proxy

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/types"

	"github.com/openfaas/faas-provider/proxy"
)

const (
	watchdogPort       = "8080"
	defaultContentType = "text/plain"
)

func NewHandlerFunc(config types.FaaSConfig, resolver proxy.BaseURLResolver) http.HandlerFunc {
	if resolver == nil {
		panic("NewHandlerFunc: empty proxy handler resolver, cannot be nil")
	}

	proxyClient := proxy.NewProxyClientFromConfig(config)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}

		switch r.Method {
		case http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodGet,
			http.MethodOptions,
			http.MethodHead:
			proxyRequest(w, r, proxyClient, resolver)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// proxyRequest handles the actual resolution of and then request to the function service.
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver proxy.BaseURLResolver) {
	ctx := originalReq.Context()

	pathVars := mux.Vars(originalReq)
	functionName := pathVars["name"]
	if functionName == "" {
		httputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
		return
	}

	functionAddr, resolveErr := resolver.Resolve(functionName)
	if resolveErr != nil {
		// TODO: Should record the 404/not found error in Prometheus.
		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, resolveErr.Error())
		httputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
		return
	}

	proxyReq, err := buildProxyRequest(originalReq, functionAddr, pathVars["params"])
	if err != nil {
		httputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
		return
	}

	if proxyReq.Body != nil {
		defer proxyReq.Body.Close()
	}

	start := time.Now()
	response, err := proxyClient.Do(proxyReq.WithContext(ctx))
	seconds := time.Since(start)

	if err != nil {
		log.Printf("error with proxy request to: %s, %s\n", proxyReq.URL.String(), err.Error())

		httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for: %s.", functionName)
		return
	}

	if response.Body != nil {
		defer response.Body.Close()
	}

	log.Printf("%s took %f seconds\n", functionName, seconds.Seconds())
	// TODO use the functionName and change active/inactive sets

	clientHeader := w.Header()
	copyHeaders(clientHeader, &response.Header)
	w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))

	w.WriteHeader(response.StatusCode)
	if response.Body != nil {
		io.Copy(w, response.Body)
	}
}

// buildProxyRequest creates a request object for the proxy request, it will ensure that
// the original request headers are preserved as well as setting openfaas system headers
func buildProxyRequest(originalReq *http.Request, baseURL url.URL, extraPath string) (*http.Request, error) {

	host := baseURL.Host
	if baseURL.Port() == "" {
		host = baseURL.Host + ":" + watchdogPort
	}

	url := url.URL{
		Scheme:   baseURL.Scheme,
		Host:     host,
		Path:     extraPath,
		RawQuery: originalReq.URL.RawQuery,
	}

	upstreamReq, err := http.NewRequest(originalReq.Method, url.String(), nil)
	if err != nil {
		return nil, err
	}
	copyHeaders(upstreamReq.Header, &originalReq.Header)

	if len(originalReq.Host) > 0 && upstreamReq.Header.Get("X-Forwarded-Host") == "" {
		upstreamReq.Header["X-Forwarded-Host"] = []string{originalReq.Host}
	}
	if upstreamReq.Header.Get("X-Forwarded-For") == "" {
		upstreamReq.Header["X-Forwarded-For"] = []string{originalReq.RemoteAddr}
	}

	if originalReq.Body != nil {
		upstreamReq.Body = originalReq.Body
	}

	return upstreamReq, nil
}

// copyHeaders clones the header values from the source into the destination.
func copyHeaders(destination http.Header, source *http.Header) {
	for k, v := range *source {
		vClone := make([]string, len(v))
		copy(vClone, v)
		destination[k] = vClone
	}
}

// getContentType resolves the correct Content-Type for a proxied function.
func getContentType(request http.Header, proxyResponse http.Header) (headerContentType string) {
	responseHeader := proxyResponse.Get("Content-Type")
	requestHeader := request.Get("Content-Type")

	if len(responseHeader) > 0 {
		headerContentType = responseHeader
	} else if len(requestHeader) > 0 {
		headerContentType = requestHeader
	} else {
		headerContentType = defaultContentType
	}

	return headerContentType
}