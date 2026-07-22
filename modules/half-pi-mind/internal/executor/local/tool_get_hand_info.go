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
			bridge := remoteBridgeFromContext(ctx)
			if bridge == nil {
				return &executor.ToolResult{Error: "远程执行系统未初始化"}
			}

			var params struct {
				HandID string `json:"hand_id"`
			}
			_ = json.Unmarshal(args, &params)

			const queryTimeout = 10 * time.Second

			if params.HandID != "" {
				return queryOneHand(ctx, bridge, params.HandID, queryTimeout)
			}
			return queryAllHands(ctx, bridge, queryTimeout)
		},
	})
}

type handInfoResult struct {
	ID      string              `json:"id"`
	OS      string              `json:"os"`
	Host    string              `json:"host"`
	WorkDir string              `json:"work_dir"`
	Tools   []protocol.ToolInfo `json:"tools"`
}

func queryOneHand(ctx context.Context, bridge *RemoteBridge, handID string, timeout time.Duration) *executor.ToolResult {
	peer := bridge.Hub.PeerByType(hub.PeerHand, handID)
	if peer == nil || peer.Type != hub.PeerHand {
		return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 不在线或不存在", handID)}
	}

	result, ok := doHandInfoQuery(ctx, bridge, handID, timeout)
	if !ok {
		return &executor.ToolResult{Error: fmt.Sprintf("获取 Hand %q 信息超时", handID)}
	}

	output, _ := json.Marshal(map[string]any{"hands": []handInfoResult{result}})
	return &executor.ToolResult{
		Success: true, Output: string(output), Data: map[string]any{"hands": []handInfoResult{result}},
		CompactFacts: handInfoCompactFacts([]handInfoResult{result}),
	}
}

func queryAllHands(ctx context.Context, bridge *RemoteBridge, timeout time.Duration) *executor.ToolResult {
	peers := bridge.Hub.PeersByType(hub.PeerHand)
	if len(peers) == 0 {
		return &executor.ToolResult{Success: true, Output: `{"hands": []}`, Data: map[string]any{"hands": []handInfoResult{}}}
	}

	var mu sync.Mutex
	var results []handInfoResult
	var wg sync.WaitGroup

	for _, p := range peers {
		wg.Add(1)
		go func(handID string) {
			defer wg.Done()
			if info, ok := doHandInfoQuery(ctx, bridge, handID, timeout); ok {
				mu.Lock()
				results = append(results, info)
				mu.Unlock()
			}
		}(p.ID)
	}
	wg.Wait()

	output, _ := json.Marshal(map[string]any{"hands": results})
	return &executor.ToolResult{
		Success: true, Output: string(output), Data: map[string]any{"hands": results},
		CompactFacts: handInfoCompactFacts(results),
	}
}

func handInfoCompactFacts(results []handInfoResult) []executor.CompactFact {
	facts := make([]executor.CompactFact, 0, len(results))
	for _, result := range results {
		toolNames := make([]string, 0, len(result.Tools))
		for _, tool := range result.Tools {
			toolNames = append(toolNames, tool.Name)
		}
		facts = append(facts, executor.CompactFact{Kind: "hand_info", HandID: result.ID, ToolNames: toolNames})
	}
	return facts
}

func doHandInfoQuery(ctx context.Context, bridge *RemoteBridge, handID string, timeout time.Duration) (handInfoResult, bool) {
	reqID := protocol.MustNewMsgID()
	ch, cancel := bridge.PendingCall(reqID, 0, handID)
	defer cancel()

	req, err := protocol.NewEnvelope("", protocol.TypeHandInfoReq, protocol.HandInfoReq{ID: reqID})
	if err != nil {
		return handInfoResult{}, false
	}
	if err := bridge.Hub.SendToType(hub.PeerHand, handID, *req); err != nil {
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
