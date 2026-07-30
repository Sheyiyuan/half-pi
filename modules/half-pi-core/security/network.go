package security

import (
	"net/netip"
	"strings"
)

var deniedNetworkPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// IsLocalNetworkHostname 判断主机名是否明确指向本机或本地网络命名空间。
func IsLocalNetworkHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".home.arpa")
}

// IsPublicNetworkAddress 判断地址是否可作为公开网络请求目标。
func IsPublicNetworkAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, prefix := range deniedNetworkPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
