package local

import (
	"context"
	"encoding/json"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "list_hands",
		Description: "列出所有在线 Hand 设备及其静态信息（OS/主机名/工作目录）。无需 RPC 查询，瞬时返回。",
		Parameters:  &executor.ObjectSchema{},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			if remoteBridge == nil {
				return &executor.ToolResult{Error: "远程执行系统未初始化"}
			}

			type handEntry struct {
				ID       string `json:"id"`
				OS       string `json:"os"`
				Hostname string `json:"hostname"`
				WorkDir  string `json:"work_dir"`
			}

			peers := remoteBridge.Hub.PeersByType(hub.PeerHand)
			hands := make([]handEntry, 0, len(peers))
			for _, p := range peers {
				entry := handEntry{ID: p.ID}
				if p.Info != nil {
					entry.OS = p.Info.OS
					entry.Hostname = p.Info.Hostname
					entry.WorkDir = p.Info.WorkDir
				}
				hands = append(hands, entry)
			}

			output, _ := json.Marshal(map[string]any{"hands": hands})
			return &executor.ToolResult{Success: true, Output: string(output)}
		},
	})
}
