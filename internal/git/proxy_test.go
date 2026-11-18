/*
Copyright 2026 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package git

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
	"gotest.tools/v3/assert"
)

func TestHTTPConnectDialer(t *testing.T) {
	t.Parallel()

	// Start a mock HTTP CONNECT proxy server
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	defer listener.Close()

	proxyAddr := listener.Addr().String()
	targetAddr := "example.com:443"

	// Track if CONNECT was received
	connectReceived := make(chan string, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the CONNECT request
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}

		// Signal that we received the CONNECT request
		connectReceived <- req.URL.String()

		// Send 200 OK response
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 Connection established",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
		}
		resp.Write(conn)

		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	}()

	// Create dialer using our implementation
	proxyURL, err := url.Parse("http://" + proxyAddr)
	assert.NilError(t, err)

	dialer := &httpConnectDialer{
		proxyURL: proxyURL,
		forward:  proxy.Direct,
	}

	// Dial through proxy
	conn, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		// Connection might close quickly, but we should have received CONNECT
		t.Logf("Dial returned error (expected): %v", err)
	}
	if conn != nil {
		conn.Close()
	}

	// Verify CONNECT request was sent
	select {
	case received := <-connectReceived:
		assert.Assert(t, strings.Contains(received, "example.com:443"),
			"Expected CONNECT to example.com:443, got: %s", received)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for CONNECT request")
	}
}

func TestHTTPConnectDialerWithAuth(t *testing.T) {
	t.Parallel()

	// Start a mock HTTP CONNECT proxy server that requires auth
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	defer listener.Close()

	proxyAddr := listener.Addr().String()
	targetAddr := "example.com:22"

	// Track auth header
	authReceived := make(chan string, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the CONNECT request
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}

		// Extract auth header
		authReceived <- req.Header.Get("Proxy-Authorization")

		// Send 200 OK response
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 Connection established",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
		}
		resp.Write(conn)

		time.Sleep(100 * time.Millisecond)
	}()

	// Create dialer with authentication
	proxyURL, err := url.Parse(fmt.Sprintf("http://testuser:testpass@%s", proxyAddr))
	assert.NilError(t, err)

	dialer := &httpConnectDialer{
		proxyURL: proxyURL,
		forward:  proxy.Direct,
	}

	// Dial through proxy
	conn, _ := dialer.Dial("tcp", targetAddr)
	if conn != nil {
		conn.Close()
	}

	// Verify auth header was sent
	select {
	case auth := <-authReceived:
		assert.Assert(t, strings.HasPrefix(auth, "Basic "),
			"Expected Basic auth header, got: %s", auth)
		// Basic dGVzdHVzZXI6dGVzdHBhc3M= is base64 of "testuser:testpass"
		assert.Equal(t, "Basic dGVzdHVzZXI6dGVzdHBhc3M=", auth)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for auth header")
	}
}

func TestHTTPConnectDialerNon200(t *testing.T) {
	t.Parallel()

	// Start a mock HTTP CONNECT proxy server that returns 403 Forbidden
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	defer listener.Close()

	proxyAddr := listener.Addr().String()
	targetAddr := "example.com:22"

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the CONNECT request
		reader := bufio.NewReader(conn)
		_, err = http.ReadRequest(reader)
		if err != nil {
			return
		}

		// Send 403 Forbidden response
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
		}
		resp.Write(conn)
	}()

	proxyURL, err := url.Parse("http://" + proxyAddr)
	assert.NilError(t, err)

	dialer := &httpConnectDialer{
		proxyURL: proxyURL,
		forward:  proxy.Direct,
	}

	conn, err := dialer.Dial("tcp", targetAddr)
	assert.Assert(t, err != nil, "expected error for non-200 proxy response")
	assert.Assert(t, conn == nil, "expected nil connection for non-200 proxy response")
	assert.Assert(t, strings.Contains(err.Error(), "403"),
		"expected error to mention 403 status, got: %s", err.Error())
}

func TestHTTPConnectDialerHTTPS(t *testing.T) {
	t.Parallel()

	// Start a plain TCP listener. The dialer should attempt a TLS
	// handshake because the proxy URL uses the https scheme. Since the
	// listener is plain TCP, the TLS handshake will not complete and the
	// context deadline will fire, proving TLS was attempted.
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	defer listener.Close()

	// Accept (and immediately close) so the TLS handshake fails fast
	// with an EOF rather than hanging.
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	proxyAddr := listener.Addr().String()
	targetAddr := "example.com:22"

	proxyURL, err := url.Parse("https://" + proxyAddr)
	assert.NilError(t, err)

	dialer := &httpConnectDialer{
		proxyURL: proxyURL,
		forward:  proxy.Direct,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if conn != nil {
		conn.Close()
	}
	// We expect an error because the plain TCP listener cannot complete a TLS handshake.
	assert.Assert(t, err != nil, "expected error when TLS handshake fails against plain TCP proxy")
}

// TestHTTPConnectDialerCancellation verifies that DialContext returns promptly
// when the context is cancelled while the proxy accepts a socket but never
// responds (exercising the watchdog goroutine).
func TestHTTPConnectDialerCancellation(t *testing.T) {
	t.Parallel()

	// Start a listener that accepts connections but never writes a response.
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Hold the connection open; never respond.
		defer conn.Close()
		<-t.Context().Done()
	}()

	proxyURL, _ := url.Parse("http://" + listener.Addr().String())
	dialer := &httpConnectDialer{proxyURL: proxyURL, forward: proxy.Direct}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", "example.com:443")
	assert.Assert(t, err != nil, "expected error on cancelled context, got nil")
	assert.Assert(t, conn == nil, "expected nil conn on cancelled context")
}

// TestRegisterHTTPConnectProxy verifies that the init()-driven dialer
// registration works. It must NOT use t.Parallel() because it validates
// package-global proxy dialer registration and must not run in parallel
// with other proxy tests that rely on that global state.
func TestRegisterHTTPConnectProxy(t *testing.T) {
	// Verify that HTTP and HTTPS schemes are registered
	// by checking if we can create dialers for these schemes
	httpURL, _ := url.Parse("http://proxy.example.com:8080")
	httpsURL, _ := url.Parse("https://proxy.example.com:8443")

	// These should not panic after registerHTTPConnectProxy is called (via init)
	httpDialer, err := proxy.FromURL(httpURL, proxy.Direct)
	assert.NilError(t, err)
	assert.Assert(t, httpDialer != nil)

	httpsDialer, err := proxy.FromURL(httpsURL, proxy.Direct)
	assert.NilError(t, err)
	assert.Assert(t, httpsDialer != nil)
}
