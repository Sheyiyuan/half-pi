package repl

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
)

func (r *Repl) handleCompact(argument string) {
	if argument == "status" {
		status, err := r.actor.CompactStatus(context.Background())
		if err != nil {
			r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("compact status: %s", compact.ErrorCodeOf(err)))
			return
		}
		printCompactStatus(status)
		return
	}
	target, err := parseCompactTarget(argument)
	if err != nil {
		r.emit(events.LevelWarn, events.TypeSystem, err.Error())
		return
	}
	requestID, err := uuid.NewV7()
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("generate Compact request ID: %v", err))
		return
	}
	ctx := requestctx.WithRequestID(context.Background(), requestID.String())
	ctx = requestctx.WithPrincipalID(ctx, fmt.Sprintf("repl:%d", os.Getpid()))
	ctx = requestctx.WithSource(ctx, "repl")
	result, err := r.actor.Compact(ctx, compact.CompactRequest{
		RequestID: requestID.String(), Principal: fmt.Sprintf("repl:%d", os.Getpid()), Target: target,
	})
	if err != nil {
		code := compact.ErrorCodeOf(err)
		if code == "" {
			code = compact.ErrInternal
		}
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("compact failed: %s", code))
		return
	}
	printCompactResult(result)
}

func parseCompactTarget(argument string) (compact.Target, error) {
	fields := strings.Fields(argument)
	switch {
	case len(fields) == 0:
		return compact.DefaultTarget{}, nil
	case len(fields) == 1 && fields[0] == "rebase":
		return compact.RebaseTarget{}, nil
	case len(fields) == 1 && strings.HasSuffix(fields[0], "%"):
		percent, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], "%"), 64)
		if err != nil || percent < 20 || percent >= 95 {
			return nil, fmt.Errorf("usage: /compact [20%%..94%%|keep 1..10000|rebase|status]")
		}
		return compact.RatioTarget{Ratio: percent / 100}, nil
	case len(fields) == 2 && fields[0] == "keep":
		messages, err := strconv.Atoi(fields[1])
		if err != nil || messages < 1 || messages > 10_000 {
			return nil, fmt.Errorf("usage: /compact keep <1..10000>")
		}
		return compact.KeepTarget{Messages: messages}, nil
	default:
		return nil, fmt.Errorf("usage: /compact [N%%|keep N|rebase|status]")
	}
}

func printCompactResult(result compact.CompactResult) {
	fmt.Printf("compact succeeded: summary=%s range=%d..%d estimated=%d->%d retained=%d..%d mode=%s context_version=%d reused=%t\n",
		result.SummaryID, result.FromSeq, result.ToSeq, result.BeforeEstimatedTokens, result.AfterEstimatedTokens,
		result.RetainedFromSeq, result.RetainedToSeq, result.GenerationMode, result.ContextVersion, result.Reused)
	switch target := result.TargetResult.(type) {
	case compact.TokenTargetResult:
		fmt.Printf("target: estimated_tokens=%d met=%t\n", target.TargetTokens, target.TargetMet)
	case compact.KeepTargetResult:
		fmt.Printf("target: keep=%d retained=%d safety_extra=%d capacity_extra=%d\n",
			target.RequestedKeepMessages, target.RetainedMessageCount, target.SafetyRetainedExtra, target.CapacityRetainedExtra)
	}
}

func printCompactStatus(status compact.CompactStatus) {
	pendingNotBefore := int64(0)
	if !status.PendingNotBefore.IsZero() {
		pendingNotBefore = status.PendingNotBefore.UnixMilli()
	}
	fmt.Printf("compact status: enabled=%t automatic=%t operation=%s estimated_tokens=%d input_budget=%d high=%d low=%d context_version=%d\n",
		status.Enabled, status.Automatic, status.OperationState, status.CurrentEstimatedTokens,
		status.InputBudget, status.HighLimit, status.LowTarget, status.ContextVersion)
	fmt.Printf("summary: id=%s range=%d..%d mode=%s bytes=%d degraded=%t blocker=%s\n",
		status.ActiveSummaryID, status.ActiveFromSeq, status.ActiveToSeq, status.ActiveGenerationMode,
		status.ActiveSummaryBytes, status.Degraded, status.Blocker)
	fmt.Printf("pending: active=%t attempt=%d not_before=%d warnings=%s\n",
		status.Pending, status.PendingAttempt, pendingNotBefore, strings.Join(status.Warnings, ","))
}
