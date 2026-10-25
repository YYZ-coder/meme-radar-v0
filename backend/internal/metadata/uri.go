// Package metadata fetches untrusted token JSON and normalizes display links.
// It writes only enrich-owned metadata; chain facts remain owned by parse.
package metadata

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// ErrBlocked identifies an address rejected by the metadata SSRF policy.
var ErrBlocked = errors.New("metadata: blocked address")

type blockedAddressError struct{ host string }

func (e *blockedAddressError) Error() string { return ErrBlocked.Error() }
func (e *blockedAddressError) Unwrap() error { return ErrBlocked }

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:2::/48"),
}

// NormalizeURI resolves IPFS references through gateway and rejects unsafe URLs.
func NormalizeURI(input, gateway string) (string, error) {
	s := strings.TrimSpace(input)
	if strings.HasPrefix(s, "ipfs://") {
		s = strings.TrimPrefix(s, "ipfs://")
		s = strings.TrimPrefix(s, "ipfs/")
		s = strings.TrimRight(gateway, "/") + "/" + s
	} else if strings.HasPrefix(s, "Qm") || strings.HasPrefix(s, "bafy") || strings.HasPrefix(s, "bafk") {
		s = strings.TrimRight(gateway, "/") + "/" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", fmt.Errorf("metadata: invalid http(s) URI")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", &blockedAddressError{host: host}
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicIP(ip) {
		return "", &blockedAddressError{host: host}
	}
	return s, nil
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Go's IsGlobalUnicast also includes shared carrier space, benchmark
	// networks and reserved Class E space, none of which is a public endpoint.
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// ExtractCID returns the CID from a bare, ipfs://, path or subdomain IPFS URI.
func ExtractCID(uri string) string {
	cid, _ := ipfsReference(uri)
	return cid
}

var cidPattern = regexp.MustCompile(`^(Qm[1-9A-HJ-NP-Za-km-z]{44}|baf[yk][a-z2-7]{20,})$`)

// ipfsReference preserves an optional object path/query for gateway fallbacks.
func ipfsReference(input string) (string, string) {
	s := strings.TrimSpace(input)
	var ref string
	switch {
	case strings.HasPrefix(s, "ipfs://"):
		ref = strings.TrimPrefix(strings.TrimPrefix(s, "ipfs://"), "ipfs/")
	case !strings.Contains(s, "://"):
		ref = s
	default:
		u, err := url.Parse(s)
		if err != nil {
			return "", ""
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", ""
		}
		if strings.HasPrefix(u.EscapedPath(), "/ipfs/") {
			ref = strings.TrimPrefix(u.EscapedPath(), "/ipfs/")
		} else if host, _, ok := strings.Cut(u.Hostname(), ".ipfs."); ok {
			ref = host + u.EscapedPath()
		}
		if ref != "" && u.RawQuery != "" {
			ref += "?" + u.RawQuery
		}
	}
	end := strings.IndexAny(ref, "/?#")
	if end < 0 {
		end = len(ref)
	}
	cid := ref[:end]
	if !cidPattern.MatchString(cid) {
		return "", ""
	}
	return cid, ref[end:]
}
