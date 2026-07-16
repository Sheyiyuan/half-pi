package hand

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

// handleRPC 执行工具（权限过滤 → 安全检查 → 执行 → 输出截断）。
func (h *Hand) handleRPC(ctx context.Context, env protocol.Envelope) {
	rpc, err := protocol.DecodePayload[protocol.RPC](&env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode rpc failed: %v\n", err)
		return
	}

	if !h.checkToolAllowed(rpc.Tool) {
		h.sendRPCReply(rpc.ID, env.MsgID, false, "",
			fmt.Sprintf("tool %q is not allowed by hand policy", rpc.Tool))
		return
	}

	args, err := json.Marshal(rpc.Args)
	if err != nil {
		h.sendRPCReply(rpc.ID, env.MsgID, false, "", fmt.Sprintf("marshal args: %v", err))
		return
	}

	// Mind 通过 use_hand 已安检 → 跳过 Hand 侧 Tool.Check / DefaultConfirm
	runner := h.trustedRunner
	if !rpc.SkipChecks {
		runner = h.checkedRunner
	}
	execCtx := ctx
	cancel := func() {}
	if rpc.TimeoutMs > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(rpc.TimeoutMs)*time.Millisecond)
	}
	defer cancel()

	result := runner.ExecuteTool(execCtx, rpc.Tool, args)

	output := result.Output
	errMsg := result.Error
	if maxSize := h.maxOutputSize(); maxSize > 0 {
		output = truncateBytes(output, maxSize)
		errMsg = truncateBytes(errMsg, maxSize)
	}

	h.sendRPCReply(rpc.ID, env.MsgID, result.Success, output, errMsg)
}

// handleHandInfoReq 返回 Hand 的动态信息（工具列表等）。
func (h *Hand) handleHandInfoReq(env protocol.Envelope) {
	req, err := protocol.DecodePayload[protocol.HandInfoReq](&env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode hand_info_req failed: %v\n", err)
		return
	}

	allTools := executor.RegisteredTools()
	tools := make([]string, 0, len(allTools))
	for _, t := range allTools {
		if h.checkToolAllowed(t.Name) {
			tools = append(tools, t.Name)
		}
	}

	info := CollectHandInfo()

	resp := protocol.HandInfoResp{
		ID:      req.ID,
		Tools:   tools,
		OS:      info.OS,
		Host:    info.Hostname,
		WorkDir: info.WorkDir,
	}

	envResp, err := protocol.NewEnvelope(env.MsgID, protocol.TypeHandInfoResp, resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create hand_info_resp failed: %v\n", err)
		return
	}
	if err := h.conn.Send(*envResp); err != nil {
		fmt.Fprintf(os.Stderr, "send hand_info_resp failed: %v\n", err)
	}
}

// checkToolAllowed 检查工具是否在权限范围内。
// 优先级：deny > allow；allow 为空时全部放行。
func (h *Hand) checkToolAllowed(name string) bool {
	if h.cfg == nil {
		return true
	}
	p := h.cfg.Hand.Permission
	for _, d := range p.DenyTools {
		if d == name {
			return false
		}
	}
	if len(p.AllowTools) == 0 {
		return true
	}
	for _, a := range p.AllowTools {
		if a == name {
			return true
		}
	}
	return false
}
