package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	client := New()
	if client.Timeout != requestTimeout {
		t.Fatalf("Timeout = %s, want %s", client.Timeout, requestTimeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", client.Transport)
	}
	if transport == http.DefaultTransport {
		t.Fatal("Transport aliases http.DefaultTransport")
	}
	if transport.DialContext == nil {
		t.Fatal("Transport.DialContext is nil")
	}
}

func TestDialResolvedFallsBackToNextAddress(t *testing.T) {
	const attemptTimeout = 10 * time.Millisecond
	ips := []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")}
	resolver := staticResolver{ips: ips}
	var addresses []string
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
	})
	dialer := dialFunc(func(ctx context.Context, _, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > attemptTimeout {
			t.Fatalf("dial context deadline = %s, want at most %s from now", deadline, attemptTimeout)
		}
		if len(addresses) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return client, nil
	})

	conn, err := dialResolved(t.Context(), resolver, dialer, attemptTimeout, "tcp", "downloads.test:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	want := []string{"192.0.2.1:443", "192.0.2.2:443"}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("dialed addresses = %v, want %v", addresses, want)
	}
}

func TestDialResolvedSkipsLookupForIPAddress(t *testing.T) {
	resolver := staticResolver{err: errors.New("lookup should not be called")}
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
	})
	dialer := dialFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("dial context has no deadline")
		}
		return client, nil
	})

	conn, err := dialResolved(t.Context(), resolver, dialer, connectTimeout, "tcp", "192.0.2.1:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

type staticResolver struct {
	ips []netip.Addr
	err error
}

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.ips, r.err
}

type dialFunc func(context.Context, string, string) (net.Conn, error)

func (f dialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}
