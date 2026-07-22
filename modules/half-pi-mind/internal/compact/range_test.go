package compact

import (
	"errors"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestSafeCutPointsKeepRoundsAndToolChainsWhole(t *testing.T) {
	messages := []store.Message{
		{Role: "user", Content: "first", RequestID: "request-1", Seq: 1},
		{Role: "assistant", Content: "done", RequestID: "request-1", Seq: 2},
		{Role: "system", Content: "legacy", Seq: 3},
		{Role: "user", Content: "second", RequestID: "request-2", Seq: 4},
		{Role: "assistant", ToolCalls: `[{"ID":"call-1","Name":"read_file","Args":"{}"}]`, RequestID: "request-2", Seq: 5},
		{Role: "tool", ToolID: "call-1", Content: "raw", RequestID: "request-2", Seq: 6},
		{Role: "assistant", Content: "done", RequestID: "request-2", Seq: 7},
		{Role: "user", Content: "current", RequestID: "request-3", Seq: 8},
	}
	rounds := buildConversationRounds(messages)
	got := safeCutPoints(rounds, 0, 0)
	if len(got) != 2 || got[0] != 3 || got[1] != 7 {
		t.Fatalf("cut points = %v", got)
	}
	protected := earliestProtectedSeq(messages, rounds, remoteexec.ProtectionSnapshot{
		Records: []remoteexec.ProtectionRecord{{Kind: "run", ID: "run-1", RequestID: "request-2"}},
	}, "request-3")
	if protected != 4 {
		t.Fatalf("protected seq = %d", protected)
	}
	got = safeCutPoints(rounds, 0, protected)
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("protected cut points = %v", got)
	}

	incomplete := append([]store.Message(nil), messages...)
	incomplete = append(incomplete[:5], incomplete[6:]...)
	got = safeCutPoints(buildConversationRounds(incomplete), 0, 0)
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("incomplete tool chain cut points = %v", got)
	}
}

func TestLegacyProtectionFallsBackConservatively(t *testing.T) {
	now := time.Now().UTC()
	messages := []store.Message{
		{Role: "user", Seq: 1, CreatedAt: now.Add(-time.Minute)},
		{Role: "assistant", Seq: 2, CreatedAt: now.Add(-time.Minute)},
		{Role: "user", Seq: 3, CreatedAt: now},
	}
	rounds := buildConversationRounds(messages)
	withTime := earliestProtectedSeq(messages, rounds, remoteexec.ProtectionSnapshot{
		Records: []remoteexec.ProtectionRecord{{Kind: "task", ID: "task-1", LegacyCreatedAt: now.Add(time.Second).UnixMilli()}},
	}, "")
	if withTime != 3 {
		t.Fatalf("legacy time protected seq = %d", withTime)
	}
	unknown := earliestProtectedSeq(messages, rounds, remoteexec.ProtectionSnapshot{
		Records: []remoteexec.ProtectionRecord{{Kind: "task", ID: "task-2", StateUnknown: true}},
	}, "")
	if unknown != 1 {
		t.Fatalf("unknown protected seq = %d", unknown)
	}
}

func TestDefaultRangeChoosesSmallestPrefixMeetingTarget(t *testing.T) {
	messages := simpleRounds(4)
	plan, err := selectRange(rangeRequest{
		Messages: messages, CurrentEstimatedTokens: 120, Target: DefaultTarget{},
		DefaultTargetTokens: 60, InputBudget: 100, HighWatermark: .8,
		Evaluate: func(toSeq int) (candidateCost, error) {
			costs := map[int]int64{2: 70, 4: 55, 6: 40}
			return candidateCost{SummaryInputTokens: int64(toSeq), WorstAfterTokens: costs[toSeq], FitsSummaryInput: true, GenerationMode: "full"}, nil
		},
	})
	if err != nil || plan.ToSeq != 4 || plan.TargetTokens != 60 {
		t.Fatalf("plan = %+v, err=%v", plan, err)
	}
}

func TestDefaultRangeFallsBackToLargestBeneficialPrefix(t *testing.T) {
	messages := simpleRounds(4)
	plan, err := selectRange(rangeRequest{
		Messages: messages, CurrentEstimatedTokens: 120, Target: DefaultTarget{},
		DefaultTargetTokens: 60, InputBudget: 100, HighWatermark: .8,
		Evaluate: func(toSeq int) (candidateCost, error) {
			costs := map[int]int64{2: 110, 4: 100, 6: 90}
			return candidateCost{SummaryInputTokens: int64(toSeq), WorstAfterTokens: costs[toSeq], FitsSummaryInput: true, GenerationMode: "full"}, nil
		},
	})
	if err != nil || plan.ToSeq != 6 || plan.Cost.WorstAfterTokens != 90 {
		t.Fatalf("fallback plan = %+v, err=%v", plan, err)
	}
}

func TestKeepRangeSeparatesSafetyAndCapacityRetention(t *testing.T) {
	messages := simpleRounds(4)
	plan, err := selectRange(rangeRequest{
		Messages: messages, CurrentEstimatedTokens: 120, Target: KeepTarget{Messages: 2},
		DefaultTargetTokens: 60, InputBudget: 100, HighWatermark: .8,
		Evaluate: func(toSeq int) (candidateCost, error) {
			return candidateCost{
				SummaryInputTokens: int64(toSeq), WorstAfterTokens: 50,
				FitsSummaryInput: toSeq <= 4, GenerationMode: "full", Blocker: ErrRebaseTooLarge,
			}, nil
		},
	})
	if err != nil || plan.ToSeq != 4 || plan.RetainedMessageCount != 4 ||
		plan.SafetyRetainedExtra != 0 || plan.CapacityRetainedExtra != 2 {
		t.Fatalf("keep plan = %+v, err=%v", plan, err)
	}
}

func TestKeepTargetCannotRestoreAlreadySummarizedMessages(t *testing.T) {
	messages := simpleRounds(3)
	_, err := selectRange(rangeRequest{
		Messages: messages, ActiveToSeq: 4, CurrentEstimatedTokens: 50, Target: KeepTarget{Messages: 4},
		DefaultTargetTokens: 60, InputBudget: 100, HighWatermark: .8,
		Evaluate: func(int) (candidateCost, error) { return candidateCost{}, nil },
	})
	var compactErr *Error
	if !errors.As(err, &compactErr) || compactErr.Code != ErrTargetUnreachable {
		t.Fatalf("error = %v", err)
	}
}

func simpleRounds(count int) []store.Message {
	messages := make([]store.Message, 0, count*2)
	for index := 0; index < count; index++ {
		requestID := "request-" + string(rune('a'+index))
		messages = append(messages,
			store.Message{Role: "user", RequestID: requestID, Seq: index*2 + 1},
			store.Message{Role: "assistant", RequestID: requestID, Seq: index*2 + 2},
		)
	}
	return messages
}
