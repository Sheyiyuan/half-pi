// Package dispatcher 统一认证并按 peer 类型分流 Mind Hub 消息。
package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// CredentialStore 提供 Hand 和 Face 分离的认证入口。
type CredentialStore interface {
	AuthenticateHandCredentialKey(label, token string) (applicationKey string, err error)
	AuthenticateFaceCredentialKey(label, token string) (applicationKey string, err error)
}

// HandHandler 接收已认证 Hand 的业务消息和断连事件。
type HandHandler interface {
	HandleHandMessage(peer *hub.Peer, env protocol.Envelope)
	HandleHandDisconnect(peer *hub.Peer)
}

// Install 将 dispatcher 安装为 Hub 回调的唯一所有者。
func Install(h *hub.Hub, credentials CredentialStore, hands HandHandler) {
	h.OnHandshake(func(key hub.PeerKey, register protocol.Register) (string, error) {
		switch key.Type {
		case hub.PeerHand:
			return credentials.AuthenticateHandCredentialKey(key.Label, register.Token)
		case hub.PeerFace:
			return credentials.AuthenticateFaceCredentialKey(key.Label, register.Token)
		default:
			return "", fmt.Errorf("unsupported peer type")
		}
	})
	h.OnMessage(func(peer *hub.Peer, env protocol.Envelope) {
		switch peer.Type {
		case hub.PeerHand:
			hands.HandleHandMessage(peer, env)
		case hub.PeerFace:
			handleFace(h, peer, env)
		}
	})
	h.OnDisconnect(func(peer *hub.Peer) {
		if peer.Type == hub.PeerHand {
			hands.HandleHandDisconnect(peer)
		}
	})
}

func handleFace(h *hub.Hub, peer *hub.Peer, env protocol.Envelope) {
	response := protocol.FaceError{
		Code:      protocol.FaceErrorInternal,
		Message:   "Face runtime is not implemented",
		Retryable: false,
	}
	if !isFaceCommand(env.Type) || protocol.ValidateFacePayload(env.Type, env.Payload) != nil {
		response.Code = protocol.FaceErrorInvalidRequest
		response.Message = "invalid Face request"
	} else {
		var meta protocol.FaceCommandMeta
		if err := json.Unmarshal(env.Payload, &meta); err == nil {
			response.RequestID = meta.RequestID
			response.ConversationID = meta.ConversationID
		}
	}
	reply, err := protocol.NewEnvelope(env.MsgID, protocol.TypeFaceError, response)
	if err != nil {
		return
	}
	if err := h.SendPeerContext(context.Background(), peer, *reply); err != nil {
		h.RemovePeer(peer)
	}
}

func isFaceCommand(typ string) bool {
	switch typ {
	case protocol.TypeFaceChat, protocol.TypeFaceChatCancel,
		protocol.TypeFaceConversationList, protocol.TypeFaceConversationCreate,
		protocol.TypeFaceConversationSnapshot, protocol.TypeFaceConversationRename,
		protocol.TypeFaceSubscribe, protocol.TypeFaceApprovalResolve,
		protocol.TypeFaceRunGet, protocol.TypeFaceRunCancel,
		protocol.TypeFaceHandList, protocol.TypeFaceHandGet,
		protocol.TypeFaceTaskList, protocol.TypeFaceTaskGet,
		protocol.TypeFaceTaskLog, protocol.TypeFaceTaskCancel:
		return true
	default:
		return false
	}
}
