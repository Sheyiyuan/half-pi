package compact

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type candidateCost struct {
	SummaryInputTokens int64
	WorstAfterTokens   int64
	GenerationMode     string
	FitsSummaryInput   bool
	Blocker            ErrorCode
}

type candidateEvaluator func(int) (candidateCost, error)

type rangeRequest struct {
	Messages               []store.Message
	ActiveToSeq            int
	MinimumToSeq           int
	AllowSameRange         bool
	CurrentEstimatedTokens int64
	Target                 Target
	DefaultTargetTokens    int64
	InputBudget            int64
	HighWatermark          float64
	Protection             remoteexec.ProtectionSnapshot
	ActiveRequestID        string
	Evaluate               candidateEvaluator
}

type rangePlan struct {
	ToSeq                 int
	RetainedFromSeq       int
	RetainedToSeq         int
	RetainedMessageCount  int
	SafetyRetainedExtra   int
	CapacityRetainedExtra int
	TargetTokens          int64
	Cost                  candidateCost
	RequiredSummaryInput  int64
	Blocker               ErrorCode
	SourceDigest          string
}

type conversationRound struct {
	startSeq       int
	endSeq         int
	effectiveCount int
	complete       bool
}

func selectRange(request rangeRequest) (rangePlan, error) {
	if request.Evaluate == nil || len(request.Messages) == 0 {
		return rangePlan{}, compactError(ErrNothingToCompact, nil)
	}
	rounds := buildConversationRounds(request.Messages)
	protectedFrom := earliestProtectedSeq(request.Messages, rounds, request.Protection, request.ActiveRequestID)
	minimumToSeq := request.ActiveToSeq
	if request.MinimumToSeq > minimumToSeq {
		minimumToSeq = request.MinimumToSeq
	}
	cutPoints := safeCutPoints(rounds, minimumToSeq, protectedFrom)
	_, explicitRebase := request.Target.(RebaseTarget)
	if (explicitRebase || request.AllowSameRange) && minimumToSeq > 0 &&
		(protectedFrom == 0 || minimumToSeq < protectedFrom) && roundEndsAt(rounds, minimumToSeq) {
		cutPoints = append(cutPoints, minimumToSeq)
		sort.Ints(cutPoints)
		cutPoints = uniqueInts(cutPoints)
	}
	if target, ok := request.Target.(KeepTarget); ok {
		return selectKeepRange(request, target, cutPoints)
	}
	if len(cutPoints) == 0 {
		return rangePlan{}, compactError(ErrNothingToCompact, nil)
	}
	switch target := request.Target.(type) {
	case DefaultTarget:
		return selectTokenRange(request, request.DefaultTargetTokens, cutPoints, request.AllowSameRange)
	case RatioTarget:
		if target.Ratio < .20 || target.Ratio >= request.HighWatermark {
			return rangePlan{}, compactError(ErrInternal, fmt.Errorf("invalid ratio target"))
		}
		return selectTokenRange(request, int64(float64(request.InputBudget)*target.Ratio), cutPoints, request.AllowSameRange)
	case RebaseTarget:
		return selectTokenRange(request, request.DefaultTargetTokens, cutPoints, true)
	default:
		return rangePlan{}, compactError(ErrInternal, fmt.Errorf("unknown compact target"))
	}
}

func selectTokenRange(request rangeRequest, targetTokens int64, cutPoints []int, rebase bool) (rangePlan, error) {
	var fallback *rangePlan
	var required int64
	var blocker ErrorCode
	for _, cut := range cutPoints {
		cost, err := request.Evaluate(cut)
		if err != nil {
			return rangePlan{}, err
		}
		if required == 0 || cost.SummaryInputTokens < required {
			required, blocker = cost.SummaryInputTokens, cost.Blocker
		}
		if !cost.FitsSummaryInput {
			continue
		}
		plan := finishRangePlan(request.Messages, cut, cost)
		plan.TargetTokens = targetTokens
		plan.RequiredSummaryInput = cost.SummaryInputTokens
		if cost.WorstAfterTokens <= targetTokens {
			return plan, nil
		}
		if rebase || cost.WorstAfterTokens < request.CurrentEstimatedTokens {
			copy := plan
			fallback = &copy
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	if required > 0 && blocker != "" {
		return rangePlan{RequiredSummaryInput: required, Blocker: blocker}, compactError(blocker, nil)
	}
	return rangePlan{}, compactError(ErrNothingToCompact, nil)
}

func selectKeepRange(request rangeRequest, target KeepTarget, cutPoints []int) (rangePlan, error) {
	if target.Messages < 1 || target.Messages > 10_000 {
		return rangePlan{}, compactError(ErrInternal, fmt.Errorf("invalid keep target"))
	}
	effective := effectiveMessagesAfter(request.Messages, request.ActiveToSeq)
	if len(effective) <= target.Messages {
		if request.ActiveToSeq > 0 && len(effective) < target.Messages {
			return rangePlan{}, compactError(ErrTargetUnreachable, nil)
		}
		return rangePlan{}, compactError(ErrNothingToCompact, nil)
	}
	firstRetained := effective[len(effective)-target.Messages].Seq
	requestedBoundary := firstRetained - 1
	start := sort.Search(len(cutPoints), func(index int) bool { return cutPoints[index] > requestedBoundary }) - 1
	if start < 0 {
		return rangePlan{}, compactError(ErrNothingToCompact, nil)
	}
	safetyRetained := countEffectiveAfter(request.Messages, cutPoints[start]) - target.Messages
	var required int64
	var blocker ErrorCode
	for index := start; index >= 0; index-- {
		cut := cutPoints[index]
		cost, err := request.Evaluate(cut)
		if err != nil {
			return rangePlan{}, err
		}
		if required == 0 || cost.SummaryInputTokens < required {
			required, blocker = cost.SummaryInputTokens, cost.Blocker
		}
		if !cost.FitsSummaryInput {
			continue
		}
		plan := finishRangePlan(request.Messages, cut, cost)
		plan.SafetyRetainedExtra = safetyRetained
		plan.CapacityRetainedExtra = plan.RetainedMessageCount - target.Messages - safetyRetained
		plan.RequiredSummaryInput = cost.SummaryInputTokens
		return plan, nil
	}
	if blocker == "" {
		blocker = ErrRebaseTooLarge
	}
	return rangePlan{RequiredSummaryInput: required, Blocker: blocker}, compactError(blocker, nil)
}

func buildConversationRounds(messages []store.Message) []conversationRound {
	var rounds []conversationRound
	var current *conversationRound
	pendingTools := make(map[string]struct{})
	valid := true
	terminal := false
	finish := func(endSeq int) {
		if current == nil {
			return
		}
		current.endSeq = endSeq
		current.complete = valid && terminal && len(pendingTools) == 0
		rounds = append(rounds, *current)
		current = nil
		pendingTools = make(map[string]struct{})
		valid, terminal = true, false
	}
	for _, message := range messages {
		if message.Role == string(llm.RoleSystem) {
			continue
		}
		if message.Role == string(llm.RoleUser) {
			if current != nil {
				finish(message.Seq - 1)
			}
			current = &conversationRound{startSeq: message.Seq, effectiveCount: 1}
			terminal = false
			continue
		}
		if current == nil {
			continue
		}
		current.effectiveCount++
		switch message.Role {
		case string(llm.RoleAssistant):
			if len(pendingTools) != 0 {
				valid = false
			}
			calls, ok := parseStoredToolCalls(message.ToolCalls)
			if !ok {
				valid = false
			}
			for _, call := range calls {
				if call.ID == "" {
					valid = false
					continue
				}
				pendingTools[call.ID] = struct{}{}
			}
			terminal = len(calls) == 0
		case string(llm.RoleTool):
			if _, ok := pendingTools[message.ToolID]; !ok {
				valid = false
			} else {
				delete(pendingTools, message.ToolID)
			}
			terminal = len(pendingTools) == 0
		default:
			valid = false
		}
	}
	if current != nil {
		finish(messages[len(messages)-1].Seq)
	}
	return rounds
}

func parseStoredToolCalls(raw string) ([]llm.ToolCall, bool) {
	if raw == "" || raw == "null" {
		return nil, true
	}
	var calls []llm.ToolCall
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		return nil, false
	}
	return calls, true
}

func safeCutPoints(rounds []conversationRound, activeToSeq, protectedFrom int) []int {
	if protectedFrom <= 0 {
		protectedFrom = int(^uint(0) >> 1)
	}
	cutPoints := make([]int, 0, len(rounds))
	for index, round := range rounds {
		if index == len(rounds)-1 || !round.complete || round.endSeq <= activeToSeq || round.endSeq >= protectedFrom {
			continue
		}
		cutPoints = append(cutPoints, round.endSeq)
	}
	return cutPoints
}

func earliestProtectedSeq(messages []store.Message, rounds []conversationRound, protection remoteexec.ProtectionSnapshot, activeRequestID string) int {
	protected := 0
	protectSeq := func(seq int) {
		if seq <= 0 {
			seq = 1
		}
		for _, round := range rounds {
			if seq >= round.startSeq && seq <= round.endSeq {
				seq = round.startSeq
				break
			}
		}
		if protected == 0 || seq < protected {
			protected = seq
		}
	}
	if activeRequestID != "" {
		if seq := firstRequestSeq(messages, activeRequestID); seq > 0 {
			protectSeq(seq)
		}
	}
	for _, record := range protection.Records {
		if record.RequestID != "" {
			seq := firstRequestSeq(messages, record.RequestID)
			if seq == 0 {
				protectSeq(1)
			} else {
				protectSeq(seq)
			}
			continue
		}
		if record.LegacyCreatedAt <= 0 {
			protectSeq(1)
			continue
		}
		anchor := latestUserBefore(messages, time.UnixMilli(record.LegacyCreatedAt))
		protectSeq(anchor)
	}
	return protected
}

func firstRequestSeq(messages []store.Message, requestID string) int {
	for _, message := range messages {
		if message.RequestID == requestID {
			return message.Seq
		}
	}
	return 0
}

func latestUserBefore(messages []store.Message, before time.Time) int {
	anchor := 0
	for _, message := range messages {
		if message.Role == string(llm.RoleUser) && !message.CreatedAt.IsZero() && !message.CreatedAt.After(before) {
			anchor = message.Seq
		}
	}
	return anchor
}

func finishRangePlan(messages []store.Message, cut int, cost candidateCost) rangePlan {
	retainedFrom, retainedTo := 0, 0
	for _, message := range messages {
		if message.Seq <= cut || message.Role == string(llm.RoleSystem) {
			continue
		}
		if retainedFrom == 0 {
			retainedFrom = message.Seq
		}
		retainedTo = message.Seq
	}
	return rangePlan{
		ToSeq: cut, RetainedFromSeq: retainedFrom, RetainedToSeq: retainedTo,
		RetainedMessageCount: countEffectiveAfter(messages, cut), Cost: cost,
	}
}

func effectiveMessagesAfter(messages []store.Message, seq int) []store.Message {
	result := make([]store.Message, 0, len(messages))
	for _, message := range messages {
		if message.Seq > seq && message.Role != string(llm.RoleSystem) {
			result = append(result, message)
		}
	}
	return result
}

func countEffectiveAfter(messages []store.Message, seq int) int {
	return len(effectiveMessagesAfter(messages, seq))
}

func roundEndsAt(rounds []conversationRound, seq int) bool {
	for _, round := range rounds {
		if round.endSeq == seq && round.complete {
			return true
		}
	}
	return false
}

func uniqueInts(values []int) []int {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
