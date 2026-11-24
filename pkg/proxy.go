package pkg

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"time"
)

// NewProxy creates a HTTP proxy to expose a host
func NewProxy(upstream string, listenPort uint32, hostIP string, timeout time.Duration, resolver Resolver) *Proxy {

	return &Proxy{
		Upstream: upstream,
		Port:     listenPort,
		HostIP:   hostIP,
		Timeout:  timeout,
		Resolver: resolver,
	}
}

// Proxy for exposing a private container
type Proxy struct {
	Timeout time.Duration

	// Port on which to listen to traffic
	Port uint32

	// Upstream is where to send traffic when received
	Upstream string

	// The IP to use to bind locally
	HostIP string

	Resolver Resolver
}

// Start listening and forwarding HTTP to the host
func (p *Proxy) Start() error {

	http.DefaultClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	upstreamHost, upstreamPort, err := getUpstream(p.Upstream, p.Port)
	if err != nil {
		return err
	}

	log.Printf("Looking up IP for: %q", upstreamHost)
	got := make(chan string, 1)

	go p.Resolver.Get(upstreamHost, got, time.Second*5)

	ipAddress := <-got
	close(got)

	upstreamAddr := fmt.Sprintf("%s:%d", ipAddress, upstreamPort)

	binds := []string{p.HostIP}
	if p.HostIP != "0.0.0.0" && p.HostIP != "127.0.0.1" && p.HostIP != "" {
		binds = append(binds, "127.0.0.1")
	}
	var listeners []net.Listener
	for _, host := range binds {
		localBind := net.JoinHostPort(host, fmt.Sprintf("%d", p.Port))
		log.Printf("Proxy from: %s, to: %s (%s)\n", localBind, p.Upstream, ipAddress)
		l, err := net.Listen("tcp4", localBind)
		if err != nil {
			log.Printf("Error: %s", err.Error())
			return err
		}
		listeners = append(listeners, l)
	}
	errCh := make(chan error, len(listeners))
	for _, ln := range listeners {
		ln := ln
		go func() {
			defer ln.Close()
			errCh <- acceptProxy(ln, upstreamAddr, p.Port)
		}()
	}
	return <-errCh
}

func acceptProxy(l net.Listener, upstreamAddr string, port uint32) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			acceptErr := fmt.Errorf("Unable to accept on %d, error: %s", port, err.Error())
			log.Printf("%s", acceptErr.Error())
			return acceptErr
		}

		upstream, err := net.Dial("tcp4", upstreamAddr)
		if err != nil {
			log.Printf("unable to dial to %s, error: %s", upstreamAddr, err.Error())
			conn.Close()
			return err
		}

		go pipe(conn, upstream)
		go pipe(upstream, conn)
	}
}

func pipe(from net.Conn, to net.Conn) {
	defer from.Close()
	io.Copy(from, to)
}

func getUpstream(val string, defaultPort uint32) (string, uint32, error) {
	upstreamHostname := val
	upstreamPort := defaultPort

	if in := strings.Index(val, ":"); in > -1 {
		upstreamHostname = val[:in]
		port, err := strconv.ParseInt(val[in+1:], 10, 32)
		if err != nil {
			return "", defaultPort, err
		}
		upstreamPort = uint32(port)
	}

	return upstreamHostname, upstreamPort, nil
}
