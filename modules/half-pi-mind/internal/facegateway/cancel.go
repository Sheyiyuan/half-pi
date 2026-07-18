package facegateway

import (
	"context"
	"errors"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

const runCancelTimeout = 4 * time.Second

func (g *Gateway) handleRunCancel(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceRunCancel](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeRunsCancel) || !g.requireConversation(state, meta) {
		return
	}
	run, found, err := g.lookupRun(request.RunID)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "run lookup failed", true)
		return
	}
	if !found || run.conversationID != request.ConversationID {
		g.sendError(state, meta, protocol.FaceErrorRunNotFound, "run was not found", false)
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationRunCancel, 0) {
		return
	}
	go g.runCancel(state, meta, request)
}

func (g *Gateway) runCancel(state *connection, meta protocol.FaceCommandMeta, request protocol.FaceRunCancel) {
	ctx, cancel := context.WithTimeout(context.Background(), runCancelTimeout)
	defer cancel()
	run, err := g.authority.CancelRun(ctx, request.ConversationID, request.RunID, request.Reason)
	if err != nil {
		code := protocol.FaceErrorInternal
		switch {
		case errors.Is(err, remoteexec.ErrRunNotFound), errors.Is(err, remoteexec.ErrRunNotOwned):
			code = protocol.FaceErrorRunNotFound
		case errors.Is(err, remoteexec.ErrRunHandOffline):
			code = protocol.FaceErrorHandOffline
		case errors.Is(err, context.DeadlineExceeded):
			code = protocol.FaceErrorTimeout
		}
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, code, "run cancellation failed")
		return
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		g.sendSuccessResult(state, meta, "Run reached a terminal state")
		return
	}
	g.sendSuccessResult(state, meta, "Run cancellation requested")
}

func (g *Gateway) handleTaskCancel(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceTaskCancel](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeTasksCancel) ||
		!g.requireScope(state, identity, meta, protocol.FaceScopeTasksRead) ||
		!g.requireConversation(state, meta) || !g.requireTask(state, meta, request.TaskID) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationTaskCancel, 0) {
		return
	}
	go g.runTaskCancel(state, meta, request)
}

func (g *Gateway) runTaskCancel(state *connection, meta protocol.FaceCommandMeta, request protocol.FaceTaskCancel) {
	ctx, cancel := context.WithTimeout(context.Background(), taskQueryTimeout)
	defer cancel()
	response, err := g.tasks.Cancel(ctx, request.ConversationID, request.TaskID, request.Reason)
	if err != nil {
		code := protocol.FaceErrorTaskStale
		switch {
		case errors.Is(err, remoteexec.ErrTaskNotOwned):
			code = protocol.FaceErrorTaskNotFound
		case errors.Is(err, remoteexec.ErrHandOffline):
			code = protocol.FaceErrorHandOffline
		case errors.Is(err, context.DeadlineExceeded):
			code = protocol.FaceErrorTimeout
		case response.Status == protocol.TaskCancelFailed:
			code = protocol.FaceErrorTaskFailed
		}
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, code, "task cancellation failed")
		return
	}
	task, taskErr := g.store.GetRemoteTask(request.TaskID)
	if response.Status != "" && taskErr == nil {
		g.sendResult(state, meta, protocol.FaceOperationTaskCancel, protocol.FaceTaskCancelResult{
			Outcome: string(response.Status), Task: projectTask(task),
		})
		return
	}
	g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "task cancellation projection failed")
}
