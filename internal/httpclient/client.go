// Package httpclient constructs the shared HTTP client used by ghget.
package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"time"
)

const (
	connectTimeout = 2 * time.Second
	requestTimeout = 30 * time.Minute
)

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// New constructs an HTTP client with a short timeout for each resolved address.
func New() *http.Client {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("http.DefaultTransport is not an *http.Transport")
	}
	transport := defaultTransport.Clone()
	d := &net.Dialer{KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialResolved(ctx, net.DefaultResolver, d, connectTimeout, network, address)
	}
	return &http.Client{Transport: transport, Timeout: requestTimeout}
}

func dialResolved(
	ctx context.Context,
	resolver resolver,
	dialer dialer,
	timeout time.Duration,
	network string,
	address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddrPort(address); err == nil {
		return dialOne(ctx, dialer, timeout, network, address)
	}
	ips, err := resolver.LookupNetIP(ctx, ipNetwork(network), host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	var firstErr error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := dialOne(ctx, dialer, timeout, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func dialOne(ctx context.Context, dialer dialer, timeout time.Duration, network, address string) (net.Conn, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return dialer.DialContext(attemptCtx, network, address)
}

func ipNetwork(network string) string {
	switch network {
	case "tcp4":
		return "ip4"
	case "tcp6":
		return "ip6"
	default:
		return "ip"
	}
}
