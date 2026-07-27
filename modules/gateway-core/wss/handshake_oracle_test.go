package wss_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

// TestUnknownLabelIsNotDistinguishableBeforeProof 断言未知 label 与已注册 label
// 在 proof 之前的握手过程一致。若 Hub 对未知 label 提前失败而不下发 challenge，
// 攻击者可无秘密地枚举出全部已注册的 Hand / Face label。
func TestUnknownLabelIsNotDistinguishableBeforeProof(t *testing.T) {
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		if key.Label != "known-hand" {
			return hub.Authentication{}, errors.New("credential not found")
		}
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
	url, closeHub := startHub(t, h)
	defer closeHub()

	// challengeFor 在给定 label 上只走到 challenge 一步，返回是否收到 challenge。
	challengeFor := func(label string) bool {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, share, err := wss.NewEphemeralShare()
		if err != nil {
			t.Fatal(err)
		}
		reg, err := protocol.NewEnvelope("", protocol.TypeRegister, protocol.Register{
			ProtocolVersion: protocol.ProtocolVersion,
			ClientID:        label,
			Type:            protocol.PeerHand,
			ClientShare:     share,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteJSON(reg); err != nil {
			t.Fatal(err)
		}
		var env protocol.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return false
		}
		return env.Type == protocol.TypeRegisterChallenge
	}

	if !challengeFor("known-hand") {
		t.Fatal("已注册 label 未收到 challenge")
	}
	if !challengeFor("no-such-hand") {
		t.Fatal("未知 label 未收到 challenge：challenge 下发泄露了 label 是否已注册")
	}

	// 未知 label 仍必须无法完成握手。
	session, err := wss.NewClient(url).ConnectAndRegister(handCredentials("no-such-hand"))
	if err == nil {
		_ = session.Conn.Close()
		t.Fatal("未知 label 完成了握手")
	}
	if !strings.Contains(err.Error(), "authentication_failed") {
		t.Fatalf("未知 label 的失败原因 = %v，应为统一的 authentication_failed", err)
	}
}
