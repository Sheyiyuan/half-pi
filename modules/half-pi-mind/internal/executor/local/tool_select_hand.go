package local

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "select_hand",
		Description: "设置或查询当前会话的默认 Hand。不传 hand_id 时返回当前值。",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "hand_id", Type: "string", Description: "要设为默认的 Hand ID，不传则查询当前值"},
			},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			bridge := remoteBridgeFromContext(ctx)
			if bridge == nil {
				return &executor.ToolResult{Error: "远程执行系统未初始化"}
			}

			var params struct {
				HandID string `json:"hand_id"`
			}
			_ = json.Unmarshal(args, &params)

			if params.HandID == "" {
				// 查询当前
				current := bridge.ActiveHand()
				msg := fmt.Sprintf("当前未设置默认 Hand。可用 list_hands 查看在线设备。")
				if current != "" {
					msg = fmt.Sprintf("当前默认 Hand: %s", current)
				}
				return &executor.ToolResult{
					Success: true,
					Output:  fmt.Sprintf(`{"active_hand": %q, "message": %q}`, current, msg),
				}
			}

			// 校验在线
			peer := bridge.Hub.Peer(params.HandID)
			if peer == nil || peer.Type != hub.PeerHand {
				return &executor.ToolResult{
					Error: fmt.Sprintf("Hand %q 不在线或不存在，用 list_hands 查看可用设备", params.HandID),
				}
			}

			if err := bridge.SetActiveHand(params.HandID); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("设置默认 Hand 失败: %v", err)}
			}

			output, _ := json.Marshal(map[string]any{
				"active_hand": params.HandID,
				"action":      "set",
			})
			return &executor.ToolResult{Success: true, Output: string(output)}
		},
	})
}
