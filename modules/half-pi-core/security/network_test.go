package security

import (
	"net/netip"
	"testing"
)

func TestIsLocalNetworkHostname(t *testing.T) {
	for _, host := range []string{"localhost", "service.localhost", "printer.local", "metadata.internal", "router.home.arpa."} {
		if !IsLocalNetworkHostname(host) {
			t.Errorf("host %q was not classified as local", host)
		}
	}
	for _, host := range []string{"example.com", "local.example.com"} {
		if IsLocalNetworkHostname(host) {
			t.Errorf("host %q was classified as local", host)
		}
	}
}

func TestIsPublicNetworkAddress(t *testing.T) {
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !IsPublicNetworkAddress(netip.MustParseAddr(raw)) {
			t.Errorf("address %s was not classified as public", raw)
		}
	}
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "192.0.2.1",
		"::1", "fe80::1", "fc00::1", "2001:db8::1", "::ffff:127.0.0.1",
	} {
		if IsPublicNetworkAddress(netip.MustParseAddr(raw)) {
			t.Errorf("address %s was classified as public", raw)
		}
	}
}
