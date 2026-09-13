package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Resolver is intentionally small so tests and installations with a custom
// DNS resolver do not need to replace global process state.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type resolutionPlan struct {
	host string
	port string
	ips  []netip.Addr
}

type planContextKey struct{}

type targetPolicy struct {
	resolver Resolver
}

var metadataAddresses = map[netip.Addr]struct{}{
	netip.MustParseAddr("169.254.169.254"): {}, // AWS, Azure, GCP, OpenStack
	netip.MustParseAddr("169.254.170.2"):   {}, // AWS ECS task credentials
	netip.MustParseAddr("100.100.100.200"): {}, // Alibaba Cloud
	netip.MustParseAddr("192.0.0.192"):     {}, // Oracle Cloud
	netip.MustParseAddr("168.63.129.16"):   {}, // Azure platform virtual IP
	netip.MustParseAddr("fd00:ec2::254"):   {}, // AWS IPv6 metadata
}

var metadataHostnames = map[string]struct{}{
	"metadata.google.internal": {},
	"metadata.goog":            {},
	"instance-data":            {},
}

// netip.Addr.IsGlobalUnicast intentionally includes documentation,
// benchmarking, carrier-grade NAT, and other special-use ranges. Those are not
// valid public status endpoints and some can route to infrastructure inside a
// hosting environment, so deny the stable special-use blocks explicitly.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::ffff:0:0/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func (p *targetPolicy) resolve(ctx context.Context, target *url.URL) (resolutionPlan, error) {
	host, port, err := validateURL(target)
	if err != nil {
		return resolutionPlan{}, err
	}

	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		if err := validateIP(literal); err != nil {
			return resolutionPlan{}, &Error{
				Kind: KindUnsafeTarget,
				Op:   "validate address",
				URL:  redactedURL(target),
				Err:  err,
			}
		}
		return resolutionPlan{host: normalizedHost(host), port: port, ips: []netip.Addr{literal}}, nil
	}

	normalized := normalizedHost(host)
	if isLocalHostname(normalized) {
		return resolutionPlan{}, &Error{
			Kind: KindUnsafeTarget,
			Op:   "validate hostname",
			URL:  redactedURL(target),
			Err:  fmt.Errorf("hostname %q is local", normalized),
		}
	}
	if _, found := metadataHostnames[normalized]; found {
		return resolutionPlan{}, &Error{
			Kind: KindUnsafeTarget,
			Op:   "validate hostname",
			URL:  redactedURL(target),
			Err:  fmt.Errorf("hostname %q is reserved for instance metadata", normalized),
		}
	}
	if !validDNSName(normalized) {
		return resolutionPlan{}, &Error{
			Kind: KindInvalidURL,
			Op:   "validate hostname",
			URL:  redactedURL(target),
			Err:  fmt.Errorf("invalid DNS name %q", normalized),
		}
	}

	addresses, err := p.resolver.LookupNetIP(ctx, "ip", normalized)
	if err != nil {
		return resolutionPlan{}, classifyResolverError(err, target)
	}
	if len(addresses) == 0 {
		return resolutionPlan{}, &Error{
			Kind: KindDNS,
			Op:   "resolve",
			URL:  redactedURL(target),
			Err:  errors.New("resolver returned no addresses"),
		}
	}

	unique := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if err := validateIP(address); err != nil {
			// Reject the entire answer if any A or AAAA record is unsafe. Picking
			// only a safe record would leave DNS rebinding and fallback paths open.
			return resolutionPlan{}, &Error{
				Kind: KindUnsafeTarget,
				Op:   "validate DNS answer",
				URL:  redactedURL(target),
				Err:  fmt.Errorf("address %q: %w", address, err),
			}
		}
		if _, found := seen[address]; found {
			continue
		}
		seen[address] = struct{}{}
		unique = append(unique, address)
	}

	return resolutionPlan{host: normalized, port: port, ips: unique}, nil
}

func validateURL(target *url.URL) (string, string, error) {
	if target == nil {
		return "", "", &Error{Kind: KindInvalidURL, Op: "validate URL", Err: errors.New("URL is nil")}
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", "", &Error{
			Kind: KindUnsupportedScheme,
			Op:   "validate URL",
			URL:  redactedURL(target),
			Err:  fmt.Errorf("scheme %q is not allowed", target.Scheme),
		}
	}
	if target.Opaque != "" || target.Host == "" {
		return "", "", &Error{
			Kind: KindInvalidURL,
			Op:   "validate URL",
			URL:  redactedURL(target),
			Err:  errors.New("an absolute hierarchical URL with a host is required"),
		}
	}
	if target.User != nil {
		return "", "", &Error{
			Kind: KindInvalidURL,
			Op:   "validate URL",
			URL:  redactedURL(target),
			Err:  errors.New("URL user information is not allowed"),
		}
	}

	host := target.Hostname()
	if host == "" {
		return "", "", &Error{Kind: KindInvalidURL, Op: "validate URL", URL: redactedURL(target), Err: errors.New("hostname is empty")}
	}
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil || portNumber == 0 {
			return "", "", &Error{
				Kind: KindInvalidURL,
				Op:   "validate URL",
				URL:  redactedURL(target),
				Err:  fmt.Errorf("invalid port %q", port),
			}
		}
		port = strconv.FormatUint(portNumber, 10)
	}
	return host, port, nil
}

func validateIP(address netip.Addr) error {
	if !address.IsValid() {
		return errors.New("invalid IP address")
	}
	if address.Is4In6() {
		return errors.New("IPv4-mapped IPv6 addresses are not allowed")
	}
	if address.Zone() != "" {
		return errors.New("scoped IPv6 addresses are not allowed")
	}
	if _, found := metadataAddresses[address]; found {
		return errors.New("instance metadata address is not allowed")
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return fmt.Errorf("special-use address range %s is not allowed", prefix)
		}
	}
	if address.IsUnspecified() {
		return errors.New("unspecified address is not allowed")
	}
	if address.IsLoopback() {
		return errors.New("loopback address is not allowed")
	}
	if address.IsPrivate() {
		return errors.New("private address is not allowed")
	}
	if address.IsLinkLocalUnicast() {
		return errors.New("link-local address is not allowed")
	}
	if address.IsMulticast() || address.IsInterfaceLocalMulticast() {
		return errors.New("multicast address is not allowed")
	}
	if !address.IsGlobalUnicast() {
		return errors.New("non-public address is not allowed")
	}
	return nil
}

func normalizedHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func isLocalHostname(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "localhost.localdomain"
}

func validDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range len(label) {
			character := label[i]
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func sameEndpoint(plan resolutionPlan, address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return false
	}
	return normalizedHost(host) == plan.host && strconv.FormatUint(portNumber, 10) == plan.port
}

func redactedURL(target *url.URL) string {
	if target == nil {
		return ""
	}
	copyURL := *target
	copyURL.User = nil
	copyURL.RawQuery = ""
	copyURL.ForceQuery = false
	copyURL.Fragment = ""
	return copyURL.String()
}

func classifyResolverError(err error, target *url.URL) error {
	kind := KindDNS
	if errors.Is(err, context.Canceled) {
		kind = KindCanceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		kind = KindTimeout
	} else {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			kind = KindTimeout
		}
	}
	return &Error{Kind: kind, Op: "resolve", URL: redactedURL(target), Err: err}
}
