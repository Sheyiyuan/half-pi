package local

import (
	"encoding/json"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// RemoteBridge 提供远程 Hand 工具所需的核心依赖。
type RemoteBridge struct {
	Hub             *hub.Hub
	ActiveHand      func() string
	SetActiveHand   func(string) error
	PendingCall     func(id string, timeout time.Duration, expectedPeer string) (<-chan protocol.Envelope, func())
	CheckAndConfirm func(toolName string, args json.RawMessage, llmConfirm bool) (bool, string)
}

var remoteBridge *RemoteBridge

// SetRemoteBridge 注入远程工具依赖。
func SetRemoteBridge(b *RemoteBridge) {
	remoteBridge = b
}
