// Package dispatcher 统一认证并按 peer 类型分流 Mind Hub 消息。
package dispatcher

import (
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// CredentialStore 提供 Hand 和 Face 分离的认证入口。
type CredentialStore interface {
	LoadHandConnectionCredential(label string) (token, applicationKey, principalID string, err error)
	LoadFaceConnectionCredential(label string) (token, applicationKey, principalID string, err error)
}

// HandHandler 接收已认证 Hand 的业务消息和断连事件。
type HandHandler interface {
	HandleHandConnect(peer *hub.Peer)
	HandleHandMessage(peer *hub.Peer, env protocol.Envelope)
	HandleHandDisconnect(peer *hub.Peer)
}

// FaceHandler 接收 Face command、连接清理和 Hand 生命周期投影。
type FaceHandler interface {
	HandleFaceMessage(peer *hub.Peer, env protocol.Envelope)
	HandleFaceDisconnect(peer *hub.Peer)
	HandleHandConnect(peer *hub.Peer)
	HandleHandDisconnect(peer *hub.Peer)
}

// Install 将 dispatcher 安装为 Hub 回调的唯一所有者。
func Install(h *hub.Hub, credentials CredentialStore, hands HandHandler, faces FaceHandler) {
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		var token, applicationKey, principalID string
		var err error
		switch key.Type {
		case hub.PeerHand:
			token, applicationKey, principalID, err = credentials.LoadHandConnectionCredential(key.Label)
		case hub.PeerFace:
			token, applicationKey, principalID, err = credentials.LoadFaceConnectionCredential(key.Label)
		default:
			err = fmt.Errorf("unsupported peer type")
		}
		return hub.Authentication{Token: token, ApplicationKey: applicationKey, PrincipalID: principalID}, err
	})
	h.OnConnect(func(peer *hub.Peer) {
		if peer.Type == hub.PeerHand {
			hands.HandleHandConnect(peer)
			faces.HandleHandConnect(peer)
		}
	})
	h.OnMessage(func(peer *hub.Peer, env protocol.Envelope) {
		switch peer.Type {
		case hub.PeerHand:
			hands.HandleHandMessage(peer, env)
		case hub.PeerFace:
			faces.HandleFaceMessage(peer, env)
		}
	})
	h.OnDisconnect(func(peer *hub.Peer) {
		switch peer.Type {
		case hub.PeerHand:
			hands.HandleHandDisconnect(peer)
			faces.HandleHandDisconnect(peer)
		case hub.PeerFace:
			faces.HandleFaceDisconnect(peer)
		}
	})
}
