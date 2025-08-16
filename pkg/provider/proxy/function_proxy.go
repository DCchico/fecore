/*
Based on "github.com/openfaas/faas-provider/proxy"
*/
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/types"

	"github.com/google/uuid"

	// "github.com/openfaas/faas-provider/proxy"

	"github.gatech.edu/faasedge/fecore/pkg/provider/handlers"
	"github.gatech.edu/faasedge/fecore/pkg/timec"
)

const (
	watchdogPort       = "8080"
	defaultContentType = "text/plain"
	dialTimeout        = 30 * time.Second  // Increased from 10s to 30s
	responseTimeout    = 30 * time.Second  // Increased from 10s to 30s
	maxIdleConns       = 100
	maxIdleConnsPerHost = 20
	idleConnTimeout    = 30 * time.Second
	maxRetries         = 20  // Increased from 5 to 20
	baseBackoff        = 100 // ms - Increased from 50ms to 100ms
	maxBackoff         = 2000 // ms - Increased from 500ms to 2000ms
)


func NewHandlerFunc(config types.FaaSConfig, resolver *handlers.InvokeResolver, fs *handlers.FunctionStore) http.HandlerFunc {
	if resolver == nil {
		panic("NewHandlerFunc: empty proxy handler resolver, cannot be nil")
	}

	// Create a custom transport with tuned parameters
	customTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			start := time.Now()
			conn, err := (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext(ctx, network, addr)
			if err != nil {
				log.Printf("[function_proxy] Connection failed to %s after %v: %v", 
					addr, time.Since(start), err)
			}
			return conn, err
		},
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   2 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Create custom client with the transport
	customClient := &http.Client{
		Transport: customTransport,
		Timeout:   responseTimeout,
	}

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
			proxyRequest(w, r, customClient, resolver, fs)

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
	// State management: get userID
	vDAGID := originalReq.Header.Get("vDAGID")
	workflowID := originalReq.Header.Get("workflowID")
	sequenceID := originalReq.Header.Get("sequence")
	isForwardDownstream := ""

	pathVars := mux.Vars(originalReq)
	functionName := pathVars["name"]
	requestID := functionName + "-" + uuid.New().String()
	firstRequestID := originalReq.Header.Get("firstRequestID")
	if firstRequestID == "" {
		firstRequestID = requestID
	}
	defer timec.RecordDuration("(function_proxy.go) proxyRequest() <requestID="+requestID+">", time.Now())
	if functionName == "" {
		httputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
		return
	}

	// Check for state-location and state-dependencies headers
	stateLocation := originalReq.Header.Get("state-location")
	stateDependencies := originalReq.Header.Get("state-dependencies")
	dependentStateLocation := originalReq.Header.Get("dependent-state-location")

	var releaseLocalState bool = false
	var reserveLocalState bool = false

	var dependencies []handlers.DependencyInfo
	if dependentStateLocation == "direct" && stateDependencies != "" {
		log.Printf("[function_proxy] Direct state access mode detected for function %s", functionName)
		log.Printf("[function_proxy] State dependencies: %s", stateDependencies)

		// Parse state-dependencies JSON
		err := json.Unmarshal([]byte(stateDependencies), &dependencies)
		if err != nil {
			log.Printf("[function_proxy] Error parsing state-dependencies: %v", err)
		} else {
			for _, dep := range dependencies {
				log.Printf("[function_proxy] Dependency - SenderVDAG: %s, SenderSeq: %s", dep.SenderVDAG, dep.SenderSeq)
			}
		}
		// Set releaseLocalState based on whether we're transitioning
		releaseLocalState = (stateLocation != "direct")
	} else if stateLocation == "direct" && dependentStateLocation != "direct" {
		// Shared Memory → Direct transition: reserve the current container for future dependencies
		log.Printf("[function_proxy] Shared Memory → Direct transition detected for function %s", functionName)
		reserveLocalState = true
	}

	dataCentricDependency := originalReq.Header.Get("data-centric-dependency")

	functionAddr, coldStart, isInitialized, resolveErr := resolver.Resolve(functionName, requestID, dependencies, vDAGID, sequenceID, releaseLocalState, reserveLocalState, dataCentricDependency)
	if resolveErr != nil {
		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, resolveErr.Error())
		httputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
		return
	}

	// Read the original request body
	var bodyBytes []byte
	if originalReq.Body != nil {
		bodyBytes, _ = io.ReadAll(originalReq.Body)
		originalReq.Body.Close() // Close original request body
	}

	maxRetries := 1000
	connectionRetries := 0
	var totalConnectTime int64

	proxyStart := time.Now()
	connStart := time.Now()

	ip := strings.Split(functionAddr.Host, ":")[0]

	fnSetupTime := time.Since(fnSetupStart).Milliseconds()
	log.Printf("[function_proxy] vDAGID = %s, workflowID = %s, fnSetupTime = %d", vDAGID, workflowID, fnSetupTime)
	/* End function replica setup */

	var response *http.Response
	var proxyReq *http.Request
	var err error
	fnExecStart := time.Now()
	totalConnectTime = -1
	backoff := baseBackoff

	for connectionRetries < maxRetries {
		connStart = time.Now()
		proxyReq, err = buildProxyRequest(originalReq, functionAddr, pathVars["params"], bodyBytes)
		if err != nil {
			httputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
			return
		}

		// Create a timeout context for this particular request
		reqCtx, cancel := context.WithTimeout(ctx, responseTimeout)
		defer cancel()

		response, err = proxyClient.Do(proxyReq.WithContext(reqCtx))
		if err != nil {
			connectionRetries++
			log.Printf("[function_proxy] Retry %d for %s: %v (cold start: %t)", connectionRetries, functionName, err, coldStart)
			
			// Calculate backoff with jitter
			jitter := rand.Intn(backoff/4)
			sleepTime := backoff + jitter
			time.Sleep(time.Duration(sleepTime) * time.Millisecond)
			
			// Increase backoff for next retry (capped)
			backoff = int(math.Min(float64(backoff*2), float64(maxBackoff)))
		} else if response.StatusCode == 500 {
			connectionRetries++
			log.Printf("[function_proxy] Retry %d for %s: received 500 status", connectionRetries, functionName)
			
			// Use same backoff strategy for 500 errors
			jitter := rand.Intn(backoff/4)
			sleepTime := backoff + jitter
			time.Sleep(time.Duration(sleepTime) * time.Millisecond)
			
			backoff = int(math.Min(float64(backoff*2), float64(maxBackoff)))
		} else if response.StatusCode == 400 {
			log.Printf("[function_proxy] Function %s returned 400 Bad Request", functionName)
			
			// Read the error response body
			if response.Body != nil {
				responseData, err := io.ReadAll(response.Body)
				if err != nil {
					log.Printf("[function_proxy] Error reading 400 response: %v", err)
				} else {
					log.Printf("[function_proxy] Function error details: %s", string(responseData))
				}
				// Important: Create a new reader for the body since we consumed it
				response.Body = io.NopCloser(bytes.NewBuffer(responseData))
			}
			
			// Exit the retry loop - we'll forward this response as is
			break
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
	if err != nil {
		log.Printf("[retryProxyRequest] Unable to contact function %s at %s: %s\n", functionName, ip, err)
		httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
		return
	}



	// For 400 errors, pass through the response with the error details
	if response.StatusCode == 400 {
		log.Printf("[function_proxy] Forwarding 400 Bad Request from function %s", functionName)
		// Update replica status regardless of response status
		ip = strings.Split(functionAddr.Host, ":")[0]
		if proxyReq != nil && proxyReq.URL != nil {
			err = fs.UpdateReplicaStatusInactive(functionName, ip, requestID)
			if err != nil {
				log.Printf("[function_proxy] Unable to update function replica status '%s': %s\n", proxyReq.URL.String(), err.Error())
				httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
				return
			}
		}
		
		// Copy headers from function response
		clientHeader := w.Header()
		copyHeaders(clientHeader, &response.Header)
		w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))
		w.Header().Set("Request-ID", requestID)
		
		// Forward the 400 status code
		w.WriteHeader(response.StatusCode)
		
		// Forward the response body
		if response.Body != nil {
			io.Copy(w, response.Body)
		}
		
		// Don't continue with normal response processing
		return
	}

	// For other non-200 responses, return a generic error
	if response.StatusCode != 200 {
		log.Printf("[retryProxyRequest] Function %s returned non-200 status: %d\n", functionName, response.StatusCode)
		httputil.Errorf(w, http.StatusInternalServerError, "Function returned status %d", response.StatusCode)
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
	if proxyReq != nil && proxyReq.URL != nil { // Check if proxyReq and proxyReq.URL are not nil
		err = fs.UpdateReplicaStatusInactive(functionName, ip, requestID)
		if err != nil {
			log.Printf("[function_proxy] Unable to update function replica status '%s': %s\n", proxyReq.URL.String(), err.Error())
			httputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for '%s'", functionName)
			return
		}
	} else {
		log.Printf("[function_proxy] proxyReq or proxyReq.URL was nil, unable to update replica status.")
	}
	// Forward the response to the downstream function
	if workflowID != "" && vDAGID != "" && isForwardDownstream != "" {
		w.WriteHeader(response.StatusCode)
		contentType := getContentType(originalReq.Header, response.Header)
		if err := sendDownstreamRequest(functionName, workflowID, vDAGID, contentType, firstRequestID, response); err != nil {
			log.Printf("[function_proxy] Error in sendDownstreamRequest: %v", err)
		}
	} else {
		clientHeader := w.Header()
		copyHeaders(clientHeader, &response.Header)
		w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))
		w.Header().Set("Request-ID", requestID)
		w.Header().Set("isInitialized", strconv.FormatBool(isInitialized))

		w.WriteHeader(response.StatusCode)
		if response.Body != nil {
			io.Copy(w, response.Body)
		}
	}
}

// buildProxyRequest creates a request object for the proxy request, it will ensure that
// the original request headers are preserved as well as setting openfaas system headers
func buildProxyRequest(originalReq *http.Request, baseURL url.URL, extraPath string, body []byte) (*http.Request, error) {
	
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

	upstreamReq, err := http.NewRequest(originalReq.Method, url.String(), bytes.NewReader(body))
	if err != nil {
		log.Printf("[ERROR] Failed to create new request: %v", err)
		return nil, err
	}
	
	copyHeaders(upstreamReq.Header, &originalReq.Header)

	if len(originalReq.Host) > 0 && upstreamReq.Header.Get("X-Forwarded-Host") == "" {
		upstreamReq.Header["X-Forwarded-Host"] = []string{originalReq.Host}
	}
	if upstreamReq.Header.Get("X-Forwarded-For") == "" {
		upstreamReq.Header["X-Forwarded-For"] = []string{originalReq.RemoteAddr}
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
