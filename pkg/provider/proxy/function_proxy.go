/*
Based on "github.com/openfaas/faas-provider/proxy"
*/
package proxy

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/types"

	"github.com/google/uuid"

	"github.com/openfaas/faas-provider/proxy"

	"github.gatech.edu/faasedge/fecore/pkg/provider/handlers"
	"github.gatech.edu/faasedge/fecore/pkg/timec"
)

const (
	watchdogPort       = "8080"
	defaultContentType = "text/plain"
)

func NewHandlerFunc(config types.FaaSConfig, resolver *handlers.InvokeResolver, fs *handlers.FunctionStore) http.HandlerFunc {
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
			proxyRequest(w, r, proxyClient, resolver, fs)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// proxyRequest handles the actual resolution of and then request to the function service.
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver *handlers.InvokeResolver, fs *handlers.FunctionStore) {
	/* Begin function replica setup */
	fnSetupStart := time.Now()
	ctx := originalReq.Context()

	pathVars := mux.Vars(originalReq)
	functionName := pathVars["name"]
	requestID := functionName + "-" + uuid.New().String()
	defer timec.RecordDuration("(function_proxy.go) proxyRequest() <requestID="+requestID+">", time.Now())
	if functionName == "" {
		httputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
		return
	}

	functionAddr, coldStart, resolveErr := resolver.Resolve(functionName, requestID)
	if resolveErr != nil {
		// TODO: Should record the 404/not found error in Prometheus.
		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, resolveErr.Error())
		httputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
		return
	}

	var proxyReq *http.Request
	var err error
	proxyReq, err = buildProxyRequest(originalReq, functionAddr, pathVars["params"])
	if err != nil {
		httputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
		return
	}

	if proxyReq.Body != nil {
		defer proxyReq.Body.Close()
	}

	var response *http.Response
	maxRetries := 1000
	connectionRetries := 0
	var totalConnectTime int64
	sleepQuantum := 1

	proxyStart := time.Now()
	connStart := time.Now()

	ip := strings.Split(functionAddr.Host, ":")[0]

	fnSetupTime := time.Since(fnSetupStart).Milliseconds()
	log.Printf("[function_proxy] fnSetupTime = %d", fnSetupTime)
	/* End function replica setup */

	fnExecStart := time.Now()
	totalConnectTime = -1
	for connectionRetries < maxRetries {
		connStart = time.Now()
		response, err = proxyClient.Do(proxyReq.WithContext(ctx))
		if err != nil {
			//log.Printf("[function_proxy] Err connecting to function: %s", err.Error())
			//sleepQuantum += 5
			connectionRetries += 1
			time.Sleep(time.Duration(sleepQuantum) * time.Millisecond)
		} else {
			break
		}
	}
	totalConnectTime = connStart.Sub(proxyStart).Milliseconds()
	fnExecTime := time.Since(fnExecStart).Milliseconds()

	if connectionRetries == maxRetries {
		log.Printf("[retryProxyRequest] Unable to contact function %s <requestID=%s> at %s: connection time exceeded\n", functionName, requestID, ip)
		httputil.Errorf(w, http.StatusInternalServerError, "Connection time exceeded")
		return
	}
	if err != nil || response.StatusCode != 200 {
		log.Printf("[retryProxyRequest] Unable to contact function %s at %s with status code %d and error : %s \n", functionName, ip, response.StatusCode, err)
		httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
		return
	}

	log.Printf("[function_proxy] Success connecting to function <requestID=%s, execTime=%d>", requestID, fnExecTime)

	proxyElapsed := time.Since(proxyStart)
	timec.RecordDuration("(function_proxy.go) proxyRequest() : [ProxyRequestTime] <requestID="+requestID+", coldStart="+strconv.FormatBool(coldStart)+", retries="+strconv.Itoa(connectionRetries)+", connectTime="+strconv.Itoa(int(totalConnectTime))+" ms>", proxyStart)

	if response.Body != nil {
		defer response.Body.Close()
	}

	log.Printf("[function_proxy] Invocation of '%s' (coldStart: %t) took %d ms with %d retries \n", functionName, coldStart, proxyElapsed.Milliseconds(), connectionRetries)
	fs.RecordInvocationTime(proxyElapsed, coldStart)
	log.Printf("[function_proxy] CALLING UpdateFunctionStats(%s, %d, %d)", functionName, fnSetupTime, fnExecTime)
	fs.UpdateFunctionStats(functionName, fnSetupTime, fnExecTime)

	ip = strings.Split(functionAddr.Host, ":")[0]
	err = fs.UpdateReplicaStatusInactive(functionName, ip, requestID)
	if err != nil {
		log.Printf("[function_proxy] Unable to update function replica status '%s': %s\n", proxyReq.URL.String(), err.Error())
		httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
		return
	}

	clientHeader := w.Header()
	copyHeaders(clientHeader, &response.Header)
	w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))
	w.Header().Set("Request-ID", requestID)

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
