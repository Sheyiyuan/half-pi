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
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/taskmanager"
)

// handleRPC 通过 Hand ToolRuntime 完成本地独立守门和执行。
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

	args, err := json.Marshal(rpc.Args)
	if err != nil {
		h.rejectRPC(rpc.RunID, protocol.RejectInvalidRequest, fmt.Sprintf("marshal args: %v", err))
		return
	}

	authorizer := &handAuthorizer{hand: h, rpc: rpc, now: now}
	runtime := executor.NewToolRuntime(authorizer, h.lifecycle)
	meta := corelifecycle.NewMeta(corelifecycle.SourceHand).WithNode(h.nodeID()).WithRequest(rpc.RunID)
	var background *executor.BackgroundContract
	if rpc.Background != nil {
		background = &executor.BackgroundContract{TaskTimeout: time.Duration(rpc.Background.MaxRuntimeMS) * time.Millisecond}
	}
	prepared, terminal := runtime.Prepare(ctx, executor.Invocation{
		Meta: meta, Tool: rpc.Tool, Args: args, TargetNode: h.nodeID(), RunID: rpc.RunID,
		Timeout: time.Until(time.UnixMilli(rpc.DeadlineAt)), Background: background,
	})
	if prepared == nil {
		code := authorizer.rejection
		if code == "" {
			code = runtimeRejectCode(terminal.ErrorCode)
		}
		h.rejectRPC(rpc.RunID, code, terminal.Output)
		return
	}
	if rpc.Background != nil {
		h.handleBackgroundRPC(rpc, prepared)
		return
	}

	execCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(rpc.DeadlineAt))
	accepted, err := h.acceptTask(rpc.RunID, rpc.Tool, cancel)
	if err != nil {
		prepared.Abort(ctx, "rpc_accept_failed")
		fmt.Fprintf(os.Stderr, "send rpc accepted failed: %v\n", err)
		return
	}
	if !accepted {
		cancel()
		prepared.Abort(ctx, "duplicate_run")
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

	var pump *progressPump
	if supportsProgress(rpc.Tool) {
		pump = newProgressPump(h, rpc.RunID, h.maxProgressSize())
		execCtx = executor.WithProgress(execCtx, pump.report)
	}
	runtimeResult := prepared.Execute(execCtx)
	if pump != nil {
		pump.close()
	}
	success := runtimeResult.ExecutionOutcome == executor.ExecutionSucceeded && runtimeResult.DeliveryOutcome == corelifecycle.OutcomeSucceeded
	output := runtimeResult.Output
	errMsg := ""
	if !success {
		errMsg, output = output, ""
	}
	truncated := false
	if maxSize := h.maxOutputSize(); maxSize > 0 {
		var outputTruncated, errorTruncated bool
		output, outputTruncated = truncateBytes(output, maxSize)
		errMsg, errorTruncated = truncateBytes(errMsg, maxSize)
		truncated = outputTruncated || errorTruncated
	}
	if h.taskCancellationRequested(rpc.RunID) && execCtx.Err() != nil && !success {
		h.finishTask(rpc.RunID, taskCancelled)
		return
	}

	if err := h.sendRPCReply(rpc.RunID, success, output, errMsg, truncated); err != nil {
		fmt.Fprintf(os.Stderr, "send rpc result failed: %v\n", err)
	}
	h.finishTask(rpc.RunID, taskDone)
}

func runtimeRejectCode(errorCode string) protocol.RejectCode {
	switch errorCode {
	case "unknown_tool":
		return protocol.RejectUnknownTool
	case "freeze_error", "transform_failed":
		return protocol.RejectInvalidRequest
	case "authorizer_denied", "guard_denied":
		return protocol.RejectCheckFailed
	default:
		return protocol.RejectInternalError
	}
}

func (h *Hand) rejectApproval(runID string, err error) {
	code := protocol.RejectApprovalRequired
	switch {
	case errors.Is(err, protocol.ErrApprovalExpired):
		code = protocol.RejectApprovalExpired
	case errors.Is(err, protocol.ErrApprovalDigestMismatch):
		code = protocol.RejectApprovalDigestMismatch
	}
	h.rejectRPC(runID, code, err.Error())
}

func (h *Hand) handleBackgroundRPC(rpc protocol.RPC, prepared *executor.PreparedExecution) {
	if h.taskManager == nil {
		h.rejectRPC(rpc.RunID, protocol.RejectInternalError, "background task manager is unavailable")
		return
	}
	digest, err := protocol.RPCApprovalDigest(rpc, h.conn.Session.LocalID)
	if err != nil {
		h.rejectRPC(rpc.RunID, protocol.RejectInvalidRequest, err.Error())
		return
	}
	task, created, err := h.taskManager.Start(rpc, digest, func(ctx context.Context, progress executor.ProgressFunc) *executor.ToolResult {
		if supportsProgress(rpc.Tool) {
			ctx = executor.WithProgress(ctx, progress)
		}
		result := prepared.Execute(ctx)
		toolResult := &executor.ToolResult{Data: result.Data}
		if result.ExecutionOutcome == executor.ExecutionSucceeded && result.DeliveryOutcome == corelifecycle.OutcomeSucceeded {
			toolResult.Success, toolResult.Output = true, result.Output
		} else {
			toolResult.Error = result.Output
		}
		return toolResult
	})
	if err != nil {
		prepared.Abort(context.Background(), "background_admission_failed")
		code := protocol.RejectInternalError
		if errors.Is(err, taskmanager.ErrConflictingDuplicate) {
			code = protocol.RejectDuplicateRun
		}
		h.rejectRPC(rpc.RunID, code, err.Error())
		return
	}
	if !created {
		prepared.Abort(context.Background(), "duplicate_background_replay")
	}
	// Background acceptance is durable queued admission; CreatedAt is the persisted admission time.
	if err := h.sendRPCMessage(protocol.TypeRPCAccepted, protocol.RPCAccepted{RunID: rpc.RunID, StartedAt: task.CreatedAt}); err != nil {
		fmt.Fprintf(os.Stderr, "send background rpc accepted failed: %v\n", err)
		return
	}
	task, err = h.taskManager.Status(rpc.RunID)
	if err != nil {
		h.rejectRPC(rpc.RunID, protocol.RejectInternalError, fmt.Sprintf("load durable task status: %v", err))
		return
	}
	if err := h.sendRPCMessage(protocol.TypeRPCResult, protocol.RPCResult{
		RunID: rpc.RunID, Success: true, TaskID: task.ID, TaskStatus: task.Status,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "send background rpc result failed: %v\n", err)
	}
}

func supportsProgress(tool string) bool {
	switch tool {
	case "exec_command", "exec_cmd", "exec_ps":
		return true
	default:
		return false
	}
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
