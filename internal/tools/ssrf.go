package tools

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// blockedHostExact are hostnames never allowed for http_get (SSRF baseline).
var blockedHostExact = map[string]struct{}{
	"localhost":                {},
	"metadata":                 {},
	"metadata.google.internal": {},
	"metadata.goog":            {},
	"instance-data":            {},
	"kubernetes.default":       {},
	"kubernetes.default.svc":   {},
}

// validateHTTPGetURLHost rejects private, link-local, and cloud-metadata targets.
// Resolves DNS when possible so raw IPs and names both fail closed.
func validateHTTPGetURLHost(ctx context.Context, host string) error {
	return validateHTTPURLHost(ctx, host, nil)
}

// validateHTTPURLHost is validateHTTPGetURLHost with an optional exact-host allowlist
// (SearXNG instance from chat.web.searxng_url; web_search only).
func validateHTTPURLHost(ctx context.Context, host string, allowHosts map[string]struct{}) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return fmt.Errorf("URL host is required")
	}
	if _, ok := allowHosts[host]; ok {
		return nil
	}
	// Strip zone id if present (fe80::1%eth0).
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	if _, blocked := blockedHostExact[host]; blocked {
		return fmt.Errorf("http_get blocked host %q (SSRF baseline)", host)
	}
	if strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("http_get blocked host %q (SSRF baseline)", host)
	}
	if strings.HasPrefix(host, "metadata.") {
		return fmt.Errorf("http_get blocked host %q (SSRF baseline)", host)
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("http_get blocked address %s (SSRF baseline)", ip)
		}
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(rctx, host)
	if err != nil {
		// Fail closed on resolution failure for non-literal hosts.
		return fmt.Errorf("http_get host resolve failed: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("http_get host %q resolved to no addresses", host)
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			return fmt.Errorf("http_get blocked unparseable address for %q", host)
		}
		ip = ip.Unmap()
		if isBlockedIP(ip) {
			return fmt.Errorf("http_get blocked address %s for host %q (SSRF baseline)", ip, host)
		}
	}
	return nil
}

func isBlockedIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	// IPv4: 0.0.0.0/8, 100.64.0.0/10 (CGNAT), 169.254.0.0/16 already link-local,
	// 192.0.0.0/24, 192.0.2.0/24, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 240.0.0.0/4
	if ip.Is4() {
		a := ip.As4()
		if a[0] == 0 {
			return true
		}
		if a[0] == 100 && a[1] >= 64 && a[1] <= 127 {
			return true
		}
		if a[0] == 192 && a[1] == 0 && (a[2] == 0 || a[2] == 2) {
			return true
		}
		if a[0] == 198 && (a[1] == 18 || a[1] == 19 || (a[1] == 51 && a[2] == 100)) {
			return true
		}
		if a[0] == 203 && a[1] == 0 && a[2] == 113 {
			return true
		}
		if a[0] >= 240 {
			return true
		}
	}
	// IPv6 ULA fc00::/7
	if ip.Is6() {
		b := ip.As16()
		if b[0]&0xfe == 0xfc {
			return true
		}
	}
	return false
}
