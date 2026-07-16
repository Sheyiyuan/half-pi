package local

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "get_hand_info",
		Description: "获取指定或全部 Hand 的详细信息（含可用工具列表）。不传 hand_id 时查询全部在线 Hand。",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "hand_id", Type: "string", Description: "Hand ID，不传则返回全部"},
			},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			if remoteBridge == nil {
				return &executor.ToolResult{Error: "远程执行系统未初始化"}
			}

			var params struct {
				HandID string `json:"hand_id"`
			}
			_ = json.Unmarshal(args, &params)

			const queryTimeout = 10 * time.Second

			if params.HandID != "" {
				return queryOneHand(ctx, params.HandID, queryTimeout)
			}
			return queryAllHands(ctx, queryTimeout)
		},
	})
}

type handInfoResult struct {
	ID      string   `json:"id"`
	OS      string   `json:"os"`
	Host    string   `json:"host"`
	WorkDir string   `json:"work_dir"`
	Tools   []string `json:"tools"`
}

func queryOneHand(ctx context.Context, handID string, timeout time.Duration) *executor.ToolResult {
	peer := remoteBridge.Hub.Peer(handID)
	if peer == nil || peer.Type != hub.PeerHand {
		return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 不在线或不存在", handID)}
	}

	result, ok := doHandInfoQuery(ctx, handID, timeout)
	if !ok {
		return &executor.ToolResult{Error: fmt.Sprintf("获取 Hand %q 信息超时", handID)}
	}

	output, _ := json.Marshal(map[string]any{"hands": []handInfoResult{result}})
	return &executor.ToolResult{Success: true, Output: string(output)}
}

func queryAllHands(ctx context.Context, timeout time.Duration) *executor.ToolResult {
	peers := remoteBridge.Hub.PeersByType(hub.PeerHand)
	if len(peers) == 0 {
		return &executor.ToolResult{Success: true, Output: `{"hands": []}`}
	}

	var mu sync.Mutex
	var results []handInfoResult
	var wg sync.WaitGroup

	for _, p := range peers {
		wg.Add(1)
		go func(handID string) {
			defer wg.Done()
			if info, ok := doHandInfoQuery(ctx, handID, timeout); ok {
				mu.Lock()
				results = append(results, info)
				mu.Unlock()
			}
		}(p.ID)
	}
	wg.Wait()

	output, _ := json.Marshal(map[string]any{"hands": results})
	return &executor.ToolResult{Success: true, Output: string(output)}
}

func doHandInfoQuery(ctx context.Context, handID string, timeout time.Duration) (handInfoResult, bool) {
	reqID := protocol.MustNewMsgID()
	ch, cancel := remoteBridge.PendingCall(reqID, 0, handID)
	defer cancel()

	req, err := protocol.NewEnvelope("", protocol.TypeHandInfoReq, protocol.HandInfoReq{ID: reqID})
	if err != nil {
		return handInfoResult{}, false
	}
	if err := remoteBridge.Hub.Send(handID, *req); err != nil {
		return handInfoResult{}, false
	}

	select {
	case env := <-ch:
		resp, err := protocol.DecodePayload[protocol.HandInfoResp](&env)
		if err != nil {
			return handInfoResult{}, false
		}
		return handInfoResult{
			ID:      handID,
			OS:      resp.OS,
			Host:    resp.Host,
			WorkDir: resp.WorkDir,
			Tools:   resp.Tools,
		}, true
	case <-time.After(timeout):
		return handInfoResult{}, false
	case <-ctx.Done():
		return handInfoResult{}, false
	}
}
