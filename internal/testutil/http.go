// Package testutil supplies a real HTTP server over in-memory connections.
// It also works in sandboxes that prohibit binding TCP sockets.
package testutil

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type listener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *listener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *listener) Addr() net.Addr { return address{} }

type address struct{}

func (address) Network() string { return "pipe" }
func (address) String() string  { return "test.local" }

func HTTPClient(t *testing.T, h http.Handler) *http.Client {
	t.Helper()
	l := &listener{connections: make(chan net.Conn), done: make(chan struct{})}
	server := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(l) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, peer := net.Pipe()
		select {
		case l.connections <- peer:
			return client, nil
		case <-ctx.Done():
			client.Close()
			peer.Close()
			return nil, ctx.Err()
		case <-l.done:
			client.Close()
			peer.Close()
			return nil, net.ErrClosed
		}
	}}
	t.Cleanup(func() { transport.CloseIdleConnections(); server.Close(); <-done })
	return &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
