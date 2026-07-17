package local

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

// RemoteBridge 提供远程 Hand 工具所需的核心依赖。
type RemoteBridge struct {
	Hub           *hub.Hub
	Runs          *remoteexec.Registry
	ActiveHand    func() string
	SessionID     func() string
	Mode          func() string
	SetActiveHand func(string) error
	// PendingCall 注册一次等待远程 Hand 响应的调用，并返回清理函数。
	PendingCall func(id string, timeout time.Duration, expectedPeer string) (<-chan protocol.Envelope, func())
	// CheckAndConfirm 复用 Mind 本地安全检查和审批流程。
	CheckAndConfirm func(toolName string, args json.RawMessage, llmConfirm bool) (bool, string)
}

type remoteBridgeKey struct{}

// WithRemoteBridge 将当前执行器的远程依赖绑定到上下文。
func WithRemoteBridge(ctx context.Context, bridge *RemoteBridge) context.Context {
	return context.WithValue(ctx, remoteBridgeKey{}, bridge)
}

func remoteBridgeFromContext(ctx context.Context) *RemoteBridge {
	bridge, _ := ctx.Value(remoteBridgeKey{}).(*RemoteBridge)
	return bridge
}
