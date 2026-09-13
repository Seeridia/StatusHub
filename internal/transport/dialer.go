package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// Dialer matches net.Dialer's DialContext method and is injectable for tests.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type pinnedDialer struct {
	base           Dialer
	connectTimeout time.Duration
}

func (d *pinnedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	plan, found := ctx.Value(planContextKey{}).(resolutionPlan)
	if !found || !sameEndpoint(plan, address) {
		return nil, &Error{
			Kind: KindUnsafeTarget,
			Op:   "dial",
			Err:  fmt.Errorf("no verified DNS plan for %q", address),
		}
	}

	dialContext := ctx
	cancel := func() {}
	if d.connectTimeout > 0 {
		dialContext, cancel = context.WithTimeout(ctx, d.connectTimeout)
	}
	defer cancel()

	var failures []error
	for _, addressIP := range plan.ips {
		if !networkSupportsAddress(network, addressIP) {
			continue
		}
		// Only the socket address changes. The HTTP request URL is intentionally
		// left untouched, so net/http keeps the original Host header and derives
		// TLS ServerName (SNI and certificate verification) from that hostname.
		pinnedAddress := net.JoinHostPort(addressIP.String(), plan.port)
		connection, err := d.base.DialContext(dialContext, network, pinnedAddress)
		if err == nil {
			return connection, nil
		}
		failures = append(failures, fmt.Errorf("dial verified address %s: %w", addressIP, err))
		if dialContext.Err() != nil {
			break
		}
	}

	err := errors.Join(failures...)
	if err == nil {
		err = errors.New("no verified address supports the requested network")
	}
	kind := KindConnect
	if errors.Is(dialContext.Err(), context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		kind = KindCanceled
	} else if errors.Is(dialContext.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		kind = KindTimeout
	} else {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			kind = KindTimeout
		}
	}
	return nil, &Error{Kind: kind, Op: "connect", Err: err}
}

func networkSupportsAddress(network string, address netip.Addr) bool {
	switch network {
	case "tcp4":
		return address.Is4()
	case "tcp6":
		return address.Is6()
	default:
		return true
	}
}
