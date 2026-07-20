package local

import (
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

const testApplicationKey = "22222222222222222222222222222222"

func enableTestHandshake(h *hub.Hub) {
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: "11111111111111111111111111111111", ApplicationKey: testApplicationKey, PrincipalID: key.Label}, nil
	})
}

func testHandCredentials(label string) wss.Credentials {
	return wss.Credentials{
		Label: label, Type: protocol.PeerHand,
		Token: "11111111111111111111111111111111", ApplicationKey: testApplicationKey,
		Info: &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "test"},
	}
}

func waitForTestHand(t *testing.T, h *hub.Hub, label string) *hub.Peer {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if peer := h.PeerByType(hub.PeerHand, label); peer != nil {
			return peer
		}
		if time.Now().After(deadline) {
			t.Fatalf("Hand %q was not promoted after registration", label)
		}
		time.Sleep(time.Millisecond)
	}
}
