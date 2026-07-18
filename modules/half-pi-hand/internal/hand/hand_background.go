package hand

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (h *Hand) handleTaskStatus(env protocol.Envelope) {
	req, err := protocol.DecodePayload[protocol.TaskStatusReq](&env)
	if err != nil || protocol.ValidateTaskStatusReq(req) != nil || h.taskManager == nil {
		return
	}
	task, err := h.taskManager.Status(req.TaskID)
	if err != nil {
		h.sendTaskError(env.MsgID, "unknown_task", err)
		return
	}
	h.sendTaskResponse(req.ID, protocol.TypeTaskStatusResp, protocol.TaskStatusResp{
		ID: req.ID, TaskID: task.ID, Tool: task.Tool, Status: task.Status,
		CreatedAt: task.CreatedAt, StartedAt: task.StartedAt, FinishedAt: task.FinishedAt,
		LogBytes: task.LogBytes, Truncated: task.Truncated, Error: task.Error,
	})
}

func (h *Hand) handleTaskLog(env protocol.Envelope) {
	req, err := protocol.DecodePayload[protocol.TaskLogReq](&env)
	if err != nil || protocol.ValidateTaskLogReq(req) != nil || h.taskManager == nil {
		return
	}
	data, next, eof, truncated, err := h.taskManager.ReadLog(req.TaskID, req.Offset, req.Limit)
	if err != nil {
		h.sendTaskError(env.MsgID, "unknown_task", err)
		return
	}
	h.sendTaskResponse(req.ID, protocol.TypeTaskLogResp, protocol.TaskLogResp{
		ID: req.ID, TaskID: req.TaskID, Offset: req.Offset, NextOffset: next,
		Data: data, EOF: eof, Truncated: truncated,
	})
}

func (h *Hand) handleTaskCancel(env protocol.Envelope) {
	req, err := protocol.DecodePayload[protocol.TaskCancel](&env)
	if err != nil || protocol.ValidateTaskCancel(req) != nil || h.taskManager == nil {
		return
	}
	status, cancelErr := h.taskManager.Cancel(req.TaskID)
	errMsg := ""
	if cancelErr != nil {
		errMsg = cancelErr.Error()
	}
	h.sendTaskResponse(req.ID, protocol.TypeTaskCancelResult, protocol.TaskCancelResult{
		ID: req.ID, TaskID: req.TaskID, Status: status, Error: errMsg,
	})
}

func (h *Hand) sendTaskMessage(typ string, payload any) {
	if err := h.sendRPCMessage(typ, payload); err != nil {
		fmt.Fprintf(os.Stderr, "send %s failed: %v\n", typ, err)
	}
}

func (h *Hand) sendTaskResponse(id, typ string, payload any) {
	if h.send != nil {
		h.sendTaskMessage(typ, payload)
		return
	}
	env, err := protocol.NewEnvelope(id, typ, payload)
	if err == nil {
		err = h.conn.Send(*env)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "send %s failed: %v\n", typ, err)
	}
}

func (h *Hand) sendTaskError(msgID, code string, err error) {
	if !isUnknownTask(err) {
		code = "internal_error"
	}
	h.sendTaskMessage(protocol.TypeError, protocol.ErrorMsg{MsgID: msgID, Code: code, Message: err.Error()})
}

func isUnknownTask(err error) bool {
	return err == sql.ErrNoRows
}
