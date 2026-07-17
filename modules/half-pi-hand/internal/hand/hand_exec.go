package hand

import (
	"context"
	"encoding/json"
	"errors"
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
		var header struct {
			RunID string `json:"run_id"`
		}
		if headerErr := json.Unmarshal(env.Payload, &header); headerErr == nil && header.RunID != "" {
			h.rejectRPC(header.RunID, protocol.RejectInvalidRequest, fmt.Sprintf("decode rpc: %v", err))
		} else {
			fmt.Fprintf(os.Stderr, "decode rpc failed: %v\n", err)
		}
		return
	}
	now := time.Now()
	if err := protocol.ValidateRPC(rpc, now); err != nil {
		code := protocol.RejectInvalidRequest
		if rpc.DeadlineAt > 0 && rpc.DeadlineAt <= now.UnixMilli() {
			code = protocol.RejectDeadlineExceeded
		}
		h.rejectRPC(rpc.RunID, code, err.Error())
		return
	}

	tool, found := executor.FindTool(rpc.Tool)
	if !found {
		h.rejectRPC(rpc.RunID, protocol.RejectUnknownTool, fmt.Sprintf("tool %q is not registered", rpc.Tool))
		return
	}
	if tool.Execute == nil {
		h.rejectRPC(rpc.RunID, protocol.RejectInternalError, fmt.Sprintf("tool %q has no execute function", rpc.Tool))
		return
	}
	if code := h.toolPermissionRejection(rpc.Tool); code != "" {
		h.rejectRPC(rpc.RunID, code, fmt.Sprintf("tool %q is not allowed by Hand policy", rpc.Tool))
		return
	}

	args, err := json.Marshal(rpc.Args)
	if err != nil {
		h.rejectRPC(rpc.RunID, protocol.RejectInvalidRequest, fmt.Sprintf("marshal args: %v", err))
		return
	}

	_, decision, reason, _ := executor.CheckTool(rpc.Tool, args)
	switch decision {
	case executor.DecisionDeny:
		h.rejectRPC(rpc.RunID, protocol.RejectCheckFailed, reason)
		return
	case executor.DecisionConfirm:
		if err := protocol.ValidateApproval(rpc.Approval, rpc.RunID, h.conn.Session.LocalID, rpc.Tool, rpc.Args, now); err != nil {
			code := protocol.RejectApprovalRequired
			switch {
			case errors.Is(err, protocol.ErrApprovalExpired):
				code = protocol.RejectApprovalExpired
			case errors.Is(err, protocol.ErrApprovalDigestMismatch):
				code = protocol.RejectApprovalDigestMismatch
			}
			h.rejectRPC(rpc.RunID, code, err.Error())
			return
		}
	}

	execCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(rpc.DeadlineAt))
	accepted, err := h.acceptTask(rpc.RunID, rpc.Tool, cancel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send rpc accepted failed: %v\n", err)
		return
	}
	if !accepted {
		cancel()
		h.rejectRPC(rpc.RunID, protocol.RejectDuplicateRun, "run_id has already been accepted")
		return
	}

	defer cancel()
	if err := execCtx.Err(); err != nil {
		if h.taskCancellationRequested(rpc.RunID) {
			h.finishTask(rpc.RunID, taskCancelled)
			return
		}
		if sendErr := h.sendRPCReply(rpc.RunID, false, "", err.Error(), false); sendErr != nil {
			fmt.Fprintf(os.Stderr, "send expired rpc result failed: %v\n", sendErr)
		}
		h.finishTask(rpc.RunID, taskDone)
		return
	}

	result := tool.Execute(execCtx, args)
	if result == nil {
		result = &executor.ToolResult{Error: "tool returned nil result"}
	}

	output := result.Output
	errMsg := result.Error
	truncated := false
	if maxSize := h.maxOutputSize(); maxSize > 0 {
		var outputTruncated, errorTruncated bool
		output, outputTruncated = truncateBytes(output, maxSize)
		errMsg, errorTruncated = truncateBytes(errMsg, maxSize)
		truncated = outputTruncated || errorTruncated
	}
	if h.taskCancellationRequested(rpc.RunID) && execCtx.Err() != nil && !result.Success {
		h.finishTask(rpc.RunID, taskCancelled)
		return
	}

	if err := h.sendRPCReply(rpc.RunID, result.Success, output, errMsg, truncated); err != nil {
		fmt.Fprintf(os.Stderr, "send rpc result failed: %v\n", err)
	}
	h.finishTask(rpc.RunID, taskDone)
}

func (h *Hand) rejectRPC(runID string, code protocol.RejectCode, reason string) {
	if err := h.sendRPCMessage(protocol.TypeRPCRejected, protocol.RPCRejected{
		RunID:  runID,
		Code:   code,
		Reason: reason,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "send rpc rejection failed: %v\n", err)
	}
}

// handleHandInfoReq 返回 Hand 的动态信息（工具列表等）。
func (h *Hand) handleHandInfoReq(env protocol.Envelope) {
	req, err := protocol.DecodePayload[protocol.HandInfoReq](&env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode hand_info_req failed: %v\n", err)
		return
	}

	allTools := executor.RegisteredTools()
	tools := make([]protocol.ToolInfo, 0, len(allTools))
	for _, t := range allTools {
		if h.checkToolAllowed(t.Name) {
			tool := t
			tools = append(tools, protocol.ToolInfo{
				Name: t.Name, Description: t.Description, Parameters: tool.SchemaParameters(),
			})
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
func (h *Hand) toolPermissionRejection(name string) protocol.RejectCode {
	if h.cfg == nil {
		return ""
	}
	p := h.cfg.Hand.Permission
	for _, d := range p.DenyTools {
		if d == name {
			return protocol.RejectDenyTools
		}
	}
	if len(p.AllowTools) == 0 {
		return ""
	}
	for _, a := range p.AllowTools {
		if a == name {
			return ""
		}
	}
	return protocol.RejectAllowToolsMiss
}

func (h *Hand) checkToolAllowed(name string) bool {
	return h.toolPermissionRejection(name) == ""
}
