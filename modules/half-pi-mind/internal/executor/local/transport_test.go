package local

import (
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

const testApplicationKey = "22222222222222222222222222222222"

func enableTestHandshake(h *hub.Hub) {
	h.OnHandshake(func(hub.PeerKey, protocol.Register) (string, error) {
		return testApplicationKey, nil
	})
}

func testHandCredentials(label string) wss.Credentials {
	return wss.Credentials{
		Label: label, Type: protocol.PeerHand,
		Token: "11111111111111111111111111111111", ApplicationKey: testApplicationKey,
		Info: &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "test"},
	}
}
