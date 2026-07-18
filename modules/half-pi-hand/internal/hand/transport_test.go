package hand

import (
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

const testApplicationKey = "22222222222222222222222222222222"

func enableTestHandshake(h *hub.Hub) {
	h.OnHandshake(func(key hub.PeerKey, _ protocol.Register) (hub.Authentication, error) {
		return hub.Authentication{ApplicationKey: testApplicationKey, PrincipalID: key.Label}, nil
	})
}

func testHandCredentials(label string) wss.Credentials {
	return wss.Credentials{
		Label: label, Type: protocol.PeerHand,
		Token: "11111111111111111111111111111111", ApplicationKey: testApplicationKey,
		Info: &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "test"},
	}
}
