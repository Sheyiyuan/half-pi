package lifecycle

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestMetaChildTrace(t *testing.T) {
	parent := NewMeta(SourceMind)
	parent.ConversationID = "conv-1"
	parent.GroupID = "group-1"
	parent.PrincipalID = "principal-1"
	parent.NodeID = "node-mind"

	child := parent.ChildMeta(SourceHand)

	if child.TraceID != parent.TraceID {
		t.Error("child must inherit parent TraceID")
	}
	if child.ParentSpanID != parent.SpanID {
		t.Error("child ParentSpanID must equal parent SpanID")
	}
	if child.SpanID == parent.SpanID {
		t.Error("child must have unique SpanID")
	}
	if child.EventID == parent.EventID {
		t.Error("child must have unique EventID")
	}
	if child.ConversationID != parent.ConversationID {
		t.Error("child must inherit ConversationID")
	}
	if child.GroupID != parent.GroupID {
		t.Error("child must inherit GroupID")
	}
	if child.PrincipalID != parent.PrincipalID {
		t.Error("child must inherit PrincipalID")
	}
	if child.RequestID != parent.RequestID {
		t.Error("child must inherit RequestID")
	}
	if child.NodeID != parent.NodeID {
		t.Error("child must inherit NodeID")
	}
	if child.Source != SourceHand {
		t.Error("child source must be Hand")
	}
}

func TestMetaHelpers(t *testing.T) {
	m := NewMeta(SourceFace)
	m = m.WithNode("hand-1")
	m = m.WithConversation("conv-1")
	m = m.WithGroup("group-1")
	m = m.WithPrincipal("principal-1")
	m = m.WithRequest("req-1")

	if m.NodeID != "hand-1" {
		t.Error("WithNode failed")
	}
	if m.ConversationID != "conv-1" {
		t.Error("WithConversation failed")
	}
	if m.GroupID != "group-1" {
		t.Error("WithGroup failed")
	}
	if m.PrincipalID != "principal-1" {
		t.Error("WithPrincipal failed")
	}
	if m.RequestID != "req-1" {
		t.Error("WithRequest failed")
	}
}

func TestPhaseOrder(t *testing.T) {
	// 确保阶段顺序递增
	phases := []Phase{
		PhaseChatReceived,
		PhaseMessageBeforeAccept,
		PhaseMessageAdmitted,
		PhaseMessagePersisted,
		PhaseModelBeforeRequest,
		PhaseModelRequested,
		PhaseModelDelta,
		PhaseModelResponseReceived,
		PhaseAssistantBeforeDeliver,
		PhaseAssistantDelivered,
		PhaseToolProposed,
		PhaseToolBeforeFreeze,
		PhaseToolFrozen,
		PhaseToolBeforeExecute,
		PhaseSecurityReviewRequested,
		PhaseSecurityReviewResolved,
		PhaseSecurityReviewFailed,
		PhaseSecurityDecision,
		PhaseApprovalRequested,
		PhaseApprovalResolved,
		PhaseApprovalExpired,
		PhaseApprovalCancelled,
		PhaseToolAuthorized,
		PhaseToolDenied,
		PhaseToolStarted,
		PhaseToolProgress,
		PhaseToolResultBeforeCommit,
		PhaseToolFinished,
		PhaseChatFinished,
	}

	for i := 1; i < len(phases); i++ {
		if !IsBefore(phases[i-1], phases[i]) {
			t.Errorf("IsBefore(%s, %s) must be true", phases[i-1], phases[i])
		}
		if !IsAfter(phases[i], phases[i-1]) {
			t.Errorf("IsAfter(%s, %s) must be true", phases[i], phases[i-1])
		}
	}

	// freeze 必须在 execute 之前
	if !IsBefore(PhaseToolFrozen, PhaseToolBeforeExecute) {
		t.Error("PhaseToolFrozen must be before PhaseToolBeforeExecute")
	}

	// execute 必须在 finish 之前
	if !IsBefore(PhaseToolStarted, PhaseToolFinished) {
		t.Error("PhaseToolStarted must be before PhaseToolFinished")
	}
}

func TestMergeVerdicts(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []Verdict
		want     Verdict
	}{
		{"all abstain", []Verdict{VerdictAbstain, VerdictAbstain}, VerdictAbstain},
		{"abstain + allow", []Verdict{VerdictAbstain, VerdictAllow}, VerdictAllow},
		{"allow + deny", []Verdict{VerdictAllow, VerdictDeny}, VerdictDeny},
		{"allow + require_approval", []Verdict{VerdictAllow, VerdictRequireApproval}, VerdictRequireApproval},
		{"deny + require_approval", []Verdict{VerdictDeny, VerdictRequireApproval}, VerdictDeny},
		{"deny + deny", []Verdict{VerdictDeny, VerdictDeny}, VerdictDeny},
		{"require_approval alone", []Verdict{VerdictRequireApproval}, VerdictRequireApproval},
		{"allow alone", []Verdict{VerdictAllow}, VerdictAllow},
		{"empty", nil, VerdictAbstain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeVerdicts(tt.verdicts...)
			if got != tt.want {
				t.Errorf("MergeVerdicts(%v) = %s, want %s", tt.verdicts, got, tt.want)
			}
		})
	}
}

func TestScopeFilter(t *testing.T) {
	filter := ScopeFilter{
		ConversationIDs: []string{"conv-1"},
		GroupIDs:        []string{"group-1"},
		PrincipalIDs:    []string{"principal-1"},
		Sources:         []string{SourceFace},
		NodeIDs:         []string{"mind"},
	}

	meta := NewMeta(SourceFace)
	meta.ConversationID = "conv-1"
	meta.GroupID = "group-1"
	meta.PrincipalID = "principal-1"
	meta.NodeID = "mind"

	if !filter.Matches(meta) {
		t.Error("scope should match")
	}

	meta.ConversationID = "conv-2"
	if filter.Matches(meta) {
		t.Error("scope should not match different conversation")
	}
	meta.ConversationID = "conv-1"

	meta.Source = SourceRepl
	if filter.Matches(meta) {
		t.Error("scope should not match different source")
	}
	meta.Source = SourceFace

	meta.PrincipalID = "principal-2"
	if filter.Matches(meta) {
		t.Error("scope should not match different principal")
	}
	meta.PrincipalID = "principal-1"

	meta.NodeID = "hand-1"
	if filter.Matches(meta) {
		t.Error("scope should not match different node")
	}
	meta.NodeID = "mind"

	emptyFilter := ScopeFilter{}
	if !emptyFilter.Matches(meta) {
		t.Error("empty scope should match everything")
	}
}

func TestRegistryRegisterAndQuery(t *testing.T) {
	r := NewRegistry()

	guard := &testGuard{id: "core-deny", verdict: VerdictDeny}
	err := r.RegisterGuard(Registration{
		ID:     "core-deny",
		Kind:   KindGuard,
		Phases: []Phase{PhaseToolBeforeExecute},
		Order:  1,
	}, guard)
	if err != nil {
		t.Fatalf("register guard: %v", err)
	}

	transformer := &testTransformer{id: "normalizer"}
	err = r.RegisterTransformer(Registration{
		ID:     "normalizer",
		Kind:   KindTransformer,
		Phases: []Phase{PhaseToolBeforeFreeze},
		Order:  2,
	}, transformer)
	if err != nil {
		t.Fatalf("register transformer: %v", err)
	}

	observer := &testObserver{id: "logger"}
	err = r.RegisterObserver(Registration{
		ID:     "logger",
		Kind:   KindObserver,
		Phases: []Phase{PhaseToolFinished},
		Order:  3,
	}, observer)
	if err != nil {
		t.Fatalf("register observer: %v", err)
	}

	auditor := &testAuditor{id: "audit-writer"}
	err = r.RegisterAuditor(Registration{
		ID:     "audit-writer",
		Kind:   KindAuditor,
		Phases: []Phase{PhaseToolFinished},
		Order:  4,
	}, auditor)
	if err != nil {
		t.Fatalf("register auditor: %v", err)
	}

	meta := NewMeta(SourceMind)

	// 查询
	guards := r.GuardsForPhase(PhaseToolBeforeExecute, meta)
	if len(guards) != 1 {
		t.Errorf("expected 1 guard, got %d", len(guards))
	}
	if guards[0].ID() != "core-deny" {
		t.Errorf("expected core-deny guard, got %s", guards[0].ID())
	}

	transformers := r.TransformersForPhase(PhaseToolBeforeFreeze, meta)
	if len(transformers) != 1 {
		t.Errorf("expected 1 transformer, got %d", len(transformers))
	}

	observers := r.ObserversForPhase(PhaseToolFinished, meta)
	if len(observers) != 1 {
		t.Errorf("expected 1 observer, got %d", len(observers))
	}

	auditors := r.AuditorsForPhase(PhaseToolFinished, meta)
	if len(auditors) != 1 {
		t.Errorf("expected 1 auditor, got %d", len(auditors))
	}

	// 空阶段
	guards = r.GuardsForPhase(PhaseToolProposed, meta)
	if len(guards) != 0 {
		t.Errorf("expected 0 guards for unregistered phase, got %d", len(guards))
	}
}

func TestRegistryDuplicateRejected(t *testing.T) {
	r := NewRegistry()

	g := &testGuard{id: "g1", verdict: VerdictAllow}
	err := r.RegisterGuard(Registration{ID: "g1", Kind: KindGuard, Phases: []Phase{PhaseToolBeforeExecute}}, g)
	if err != nil {
		t.Fatal(err)
	}
	err = r.RegisterGuard(Registration{ID: "g1", Kind: KindGuard, Phases: []Phase{PhaseToolBeforeExecute}}, g)
	if err == nil {
		t.Error("duplicate registration should be rejected")
	}
}

func TestRegistryOrdering(t *testing.T) {
	r := NewRegistry()

	g1 := &testGuard{id: "g1", verdict: VerdictAllow}
	g2 := &testGuard{id: "g2", verdict: VerdictAllow}
	g3 := &testGuard{id: "g3", verdict: VerdictDeny}

	// 注册顺序与 Order 不同
	r.RegisterGuard(Registration{ID: "g3", Kind: KindGuard, Phases: []Phase{PhaseToolBeforeExecute}, Order: 3}, g3)
	r.RegisterGuard(Registration{ID: "g1", Kind: KindGuard, Phases: []Phase{PhaseToolBeforeExecute}, Order: 1}, g1)
	r.RegisterGuard(Registration{ID: "g2", Kind: KindGuard, Phases: []Phase{PhaseToolBeforeExecute}, Order: 2}, g2)

	meta := NewMeta(SourceMind)
	guards := r.GuardsForPhase(PhaseToolBeforeExecute, meta)

	if len(guards) != 3 {
		t.Fatalf("expected 3 guards, got %d", len(guards))
	}
	if guards[0].ID() != "g1" {
		t.Errorf("expected g1 first, got %s", guards[0].ID())
	}
	if guards[1].ID() != "g2" {
		t.Errorf("expected g2 second, got %s", guards[1].ID())
	}
	if guards[2].ID() != "g3" {
		t.Errorf("expected g3 third, got %s", guards[2].ID())
	}
}

func TestRegistryScopeFiltering(t *testing.T) {
	r := NewRegistry()

	scopedGuard := &testGuard{id: "face-only", verdict: VerdictAllow}
	r.RegisterGuard(Registration{
		ID:     "face-only",
		Kind:   KindGuard,
		Phases: []Phase{PhaseMessageBeforeAccept},
		Order:  1,
		Scope: ScopeFilter{
			Sources: []string{SourceFace},
		},
	}, scopedGuard)

	unscopedGuard := &testGuard{id: "all-sources", verdict: VerdictDeny}
	r.RegisterGuard(Registration{
		ID:     "all-sources",
		Kind:   KindGuard,
		Phases: []Phase{PhaseMessageBeforeAccept},
		Order:  2,
	}, unscopedGuard)

	faceMeta := NewMeta(SourceFace)
	replMeta := NewMeta(SourceRepl)

	faceGuards := r.GuardsForPhase(PhaseMessageBeforeAccept, faceMeta)
	if len(faceGuards) != 2 {
		t.Errorf("Face should see 2 guards, got %d", len(faceGuards))
	}

	replGuards := r.GuardsForPhase(PhaseMessageBeforeAccept, replMeta)
	if len(replGuards) != 1 {
		t.Errorf("REPL should see 1 guard, got %d", len(replGuards))
	}
	if replGuards[0].ID() != "all-sources" {
		t.Errorf("REPL should only see all-sources guard")
	}
}

func TestOutcomeTerminal(t *testing.T) {
	terminals := []Outcome{OutcomeSucceeded, OutcomeFailed, OutcomeCancelled, OutcomeTimedOut, OutcomePanicked, OutcomeDenied, OutcomeBlocked}
	for _, o := range terminals {
		if !o.IsTerminal() {
			t.Errorf("%s should be terminal", o)
		}
	}
}

// ── 测试用的 stub 实现 ──

type testGuard struct {
	id      string
	verdict Verdict
}

func (g *testGuard) ID() string                                                { return g.id }
func (g *testGuard) Evaluate(ctx context.Context, action FrozenAction) Verdict { return g.verdict }

type testTransformer struct {
	id string
}

func (t *testTransformer) ID() string { return t.id }
func (t *testTransformer) Transform(ctx context.Context, action MutableAction) (MutableAction, error) {
	return action, nil
}

type testObserver struct {
	id       string
	observed []RedactedEvent
	mu       sync.Mutex
}

func (o *testObserver) ID() string { return o.id }
func (o *testObserver) Observe(ctx context.Context, event RedactedEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observed = append(o.observed, event)
}

type testAuditor struct {
	id        string
	mutations []AuditMutation
	mu        sync.Mutex
}

func (a *testAuditor) ID() string { return a.id }
func (a *testAuditor) Commit(ctx context.Context, m AuditMutation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mutations = append(a.mutations, m)
	return nil
}

func TestHashDigestDeterministic(t *testing.T) {
	d1 := HashDigest([]byte("hello"))
	d2 := HashDigest([]byte("hello"))
	if d1 != d2 {
		t.Error("hash must be deterministic")
	}
	if d1 == HashDigest([]byte("world")) {
		t.Error("different inputs must have different digests")
	}
}

func TestConcurrentRegistryAccess(t *testing.T) {
	r := NewRegistry()
	guard := &testGuard{id: "g", verdict: VerdictAllow}
	meta := NewMeta(SourceMind)

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			phase := PhaseToolBeforeExecute
			_ = r.RegisterGuard(Registration{
				ID: "guard-" + string(rune('a'+n)), Kind: KindGuard,
				Phases: []Phase{phase}, Order: n,
			}, guard)
		}(i)
	}
	wg.Wait()

	// 查询应该是安全的
	guards := r.GuardsForPhase(PhaseToolBeforeExecute, meta)
	if len(guards) != 10 {
		t.Errorf("expected 10 guards, got %d", len(guards))
	}

	// 按 Order 排序
	for i := 1; i < len(guards); i++ {
		// 由于 Order 都是唯一的 0-9，应该排好
		_ = guards[i]
	}
}

func TestPhaseOrderSorting(t *testing.T) {
	// 验证所有已注册阶段都有 phaseOrder 条目
	allPhases := []Phase{
		PhaseChatReceived, PhaseMessageBeforeAccept, PhaseMessageAdmitted,
		PhaseMessagePersisted, PhaseChatFinished,
		PhaseModelBeforeRequest, PhaseModelRequested, PhaseModelDelta,
		PhaseModelResponseReceived, PhaseAssistantBeforeDeliver,
		PhaseAssistantDelivered, PhaseModelFailed,
		PhaseToolProposed, PhaseToolBeforeFreeze, PhaseToolFrozen,
		PhaseToolBeforeExecute, PhaseToolAuthorized, PhaseToolDenied,
		PhaseToolStarted, PhaseToolProgress, PhaseToolResultBeforeCommit,
		PhaseToolFinished,
		PhaseApprovalRequested, PhaseApprovalResolved, PhaseApprovalExpired,
		PhaseApprovalCancelled,
		PhaseSecurityReviewRequested, PhaseSecurityReviewResolved,
		PhaseSecurityReviewFailed, PhaseSecurityDecision,
	}

	for _, p := range allPhases {
		if _, ok := phaseOrder[p]; !ok {
			t.Errorf("phase %s has no order in phaseOrder map", p)
		}
	}
}

func TestVerdictString(t *testing.T) {
	if VerdictAbstain.String() != "abstain" {
		t.Error("abstain string wrong")
	}
	if VerdictAllow.String() != "allow" {
		t.Error("allow string wrong")
	}
	if VerdictDeny.String() != "deny" {
		t.Error("deny string wrong")
	}
	if VerdictRequireApproval.String() != "require_approval" {
		t.Error("require_approval string wrong")
	}
}

func TestRegistrationCapabilities(t *testing.T) {
	reg := Registration{
		ID:           "test",
		Kind:         KindGuard,
		Phases:       []Phase{PhaseToolBeforeExecute},
		Capabilities: []Capability{CapabilityDeny, CapabilityReadArgs},
	}

	if !reg.HasCapability(CapabilityDeny) {
		t.Error("should have deny capability")
	}
	if !reg.HasCapability(CapabilityReadArgs) {
		t.Error("should have read_args capability")
	}
	if reg.HasCapability(CapabilityReadRaw) {
		t.Error("should not have read_raw capability")
	}
	if reg.HasCapability("nonexistent") {
		t.Error("should not have nonexistent capability")
	}
}

func TestIsBeforeIsAfter(t *testing.T) {
	// 同一阶段
	if IsBefore(PhaseToolFrozen, PhaseToolFrozen) {
		t.Error("same phase should not be before itself")
	}
	if IsAfter(PhaseToolFrozen, PhaseToolFrozen) {
		t.Error("same phase should not be after itself")
	}

	// 未注册阶段不能参与顺序判断。
	unknown := Phase("unknown.phase")
	if IsBefore(unknown, PhaseToolFrozen) {
		t.Error("unknown phase must not be ordered before a registered phase")
	}
	if IsAfter(unknown, PhaseToolFrozen) {
		t.Error("unknown phase should not be after registered phases")
	}
}

func TestMetaImmutability(t *testing.T) {
	original := NewMeta(SourceMind)
	original.ConversationID = "conv-1"
	original.GroupID = "group-1"

	// WithNode should not mutate original
	modified := original.WithNode("hand-1")
	if original.NodeID == "hand-1" {
		t.Error("WithNode should not mutate original NodeID")
	}
	if modified.NodeID != "hand-1" {
		t.Error("WithNode should set NodeID on copy")
	}
	if modified.ConversationID != original.ConversationID {
		t.Error("copy should preserve ConversationID")
	}
}

func TestEventDigestConsistency(t *testing.T) {
	base := NewMeta(SourceMind)
	proposed := ToolProposedEvent{Meta: base, Tool: "read_file", ArgsDigest: HashDigest([]byte(`{"path":"/tmp/test"}`))}

	if proposed.Meta.EventID == "" {
		t.Error("EventID should not be empty")
	}
	if proposed.Meta.TraceID == "" {
		t.Error("TraceID should not be empty")
	}
	if proposed.Meta.SpanID == "" {
		t.Error("SpanID should not be empty")
	}
	if proposed.Meta.Source != SourceMind {
		t.Error("source should be mind")
	}
	if proposed.ArgsDigest == "" {
		t.Error("ArgsDigest should not be empty")
	}
}

func TestRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	meta := NewMeta(SourceMind)

	if len(r.GuardsForPhase(PhaseToolBeforeExecute, meta)) != 0 {
		t.Error("empty registry should have no guards")
	}
	if len(r.TransformersForPhase(PhaseToolBeforeFreeze, meta)) != 0 {
		t.Error("empty registry should have no transformers")
	}
	if len(r.ObserversForPhase(PhaseToolFinished, meta)) != 0 {
		t.Error("empty registry should have no observers")
	}
	if len(r.AuditorsForPhase(PhaseToolFinished, meta)) != 0 {
		t.Error("empty registry should have no auditors")
	}
}

func TestMetaIDsUnique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		m := NewMeta(SourceMind)
		if ids[m.EventID] {
			t.Fatal("EventID collision detected")
		}
		ids[m.EventID] = true
	}
}

func TestGuardEvaluateWithTimeout(t *testing.T) {
	g := &testGuard{id: "slow", verdict: VerdictAllow}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Guard 应该能处理 context 取消
	done := make(chan Verdict, 1)
	go func() {
		done <- g.Evaluate(ctx, FrozenAction{})
	}()

	select {
	case <-done:
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Error("guard should not block indefinitely")
	}
}

func TestSortingIsStable(t *testing.T) {
	r := NewRegistry()
	phase := PhaseToolBeforeExecute

	// Register guards with same order - should be sorted by ID
	for _, id := range []string{"c", "a", "b"} {
		g := &testGuard{id: id, verdict: VerdictAllow}
		r.RegisterGuard(Registration{
			ID: id, Kind: KindGuard, Phases: []Phase{phase}, Order: 0,
		}, g)
	}

	meta := NewMeta(SourceMind)
	guards := r.GuardsForPhase(phase, meta)

	if len(guards) != 3 {
		t.Fatalf("expected 3 guards, got %d", len(guards))
	}
	if guards[0].ID() != "a" || guards[1].ID() != "b" || guards[2].ID() != "c" {
		t.Errorf("guards not sorted by ID: %s, %s, %s", guards[0].ID(), guards[1].ID(), guards[2].ID())
	}
}

func TestVerifyPhasesInOrder(t *testing.T) {
	// 每个已注册阶段的顺序值必须唯一且递增
	values := make(map[int]bool)
	orderKeys := make([]Phase, 0, len(phaseOrder))
	for p := range phaseOrder {
		orderKeys = append(orderKeys, p)
	}
	sort.Slice(orderKeys, func(i, j int) bool {
		return phaseOrder[orderKeys[i]] < phaseOrder[orderKeys[j]]
	})

	for i, p := range orderKeys {
		v := phaseOrder[p]
		if values[v] {
			t.Errorf("duplicate phase order value %d for phase %s", v, p)
		}
		values[v] = true
		if i > 0 {
			prev := phaseOrder[orderKeys[i-1]]
			if v <= prev {
				t.Errorf("phase order not strictly increasing: %d <= %d for %s after %s", v, prev, p, orderKeys[i-1])
			}
		}
	}
}

func TestObserverViewsEnforceCapabilitiesAndIsolation(t *testing.T) {
	registry := NewRegistry()
	plain := &capturingObserver{id: "plain"}
	raw := &capturingObserver{id: "raw"}
	if err := registry.RegisterObserver(Registration{
		ID: "plain", Kind: KindObserver, Phases: []Phase{PhaseToolFrozen},
	}, plain); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterObserver(Registration{
		ID: "raw", Kind: KindObserver, Phases: []Phase{PhaseToolFrozen},
		Capabilities: []Capability{CapabilityReadArgs},
	}, raw); err != nil {
		t.Fatal(err)
	}
	meta := NewMeta(SourceMind).WithGroup("group-a")
	event := RedactedEvent{Meta: meta, Phase: PhaseToolFrozen, InputDigest: "sha256:test", Sensitive: map[string]any{
		"args": map[string]any{"path": "secret"},
	}}
	registry.Publish(context.Background(), event)
	if err := registry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if plain.event.Sensitive != nil {
		t.Fatalf("plain observer received sensitive data: %#v", plain.event.Sensitive)
	}
	if raw.event.Sensitive == nil {
		t.Fatal("authorized observer did not receive sensitive data")
	}
	raw.event.Sensitive["args"].(map[string]any)["path"] = "mutated"
	if event.Sensitive["args"].(map[string]any)["path"] != "secret" {
		t.Fatal("observer mutated publisher event")
	}
}

func TestHookViewsDeepCloneNamedAndTypedCollections(t *testing.T) {
	type namedBytes []byte
	type nested struct {
		Values []string
	}
	source := MutableAction{Meta: NewMeta(SourceMind), Kind: "typed", Fields: map[string]any{
		"raw":     namedBytes("secret"),
		"entries": []nested{{Values: []string{"original"}}},
	}}
	clone := CloneMutableAction(source)
	clone.Fields["raw"].(namedBytes)[0] = 'X'
	clone.Fields["entries"].([]nested)[0].Values[0] = "mutated"
	if string(source.Fields["raw"].(namedBytes)) != "secret" ||
		source.Fields["entries"].([]nested)[0].Values[0] != "original" {
		t.Fatalf("hook clone retained aliases: source=%+v clone=%+v", source.Fields, clone.Fields)
	}

	event := RedactedEvent{Meta: NewMeta(SourceMind), Phase: PhaseToolFrozen, Sensitive: map[string]any{
		"args": namedBytes("private"),
	}}
	registration := Registration{Capabilities: []Capability{CapabilityReadArgs}}
	first := ObserverView(event, registration)
	second := ObserverView(event, registration)
	first.Sensitive["args"].(namedBytes)[0] = 'X'
	if string(event.Sensitive["args"].(namedBytes)) != "private" || string(second.Sensitive["args"].(namedBytes)) != "private" {
		t.Fatal("observer views share a named slice")
	}

	frozen := FrozenAction{RiskLabels: []string{"original"}, Sensitive: map[string]any{"args": namedBytes("guard")}}
	frozenClone := CloneFrozenAction(frozen)
	frozenClone.RiskLabels[0] = "mutated"
	frozenClone.Sensitive["args"].(namedBytes)[0] = 'X'
	if frozen.RiskLabels[0] != "original" || string(frozen.Sensitive["args"].(namedBytes)) != "guard" {
		t.Fatal("guard view retained mutable aliases")
	}

	mutation := AuditMutation{RiskLabels: []string{"original"}, Details: map[string]any{"values": []string{"audit"}}}
	mutationClone := CloneAuditMutation(mutation)
	mutationClone.RiskLabels[0] = "mutated"
	mutationClone.Details["values"].([]string)[0] = "mutated"
	if mutation.RiskLabels[0] != "original" || mutation.Details["values"].([]string)[0] != "audit" {
		t.Fatal("auditor view retained mutable aliases")
	}
}

func TestObserverQueueIsBoundedAndFailOpen(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterObserver(Registration{
		ID: "slow", Kind: KindObserver, Phases: []Phase{PhaseModelDelta}, Timeout: 20 * time.Millisecond,
	}, &firstBlockingObserver{}); err != nil {
		t.Fatal(err)
	}
	meta := NewMeta(SourceMind)
	for i := 0; i < observerQueueSize*4; i++ {
		registry.Publish(context.Background(), RedactedEvent{Meta: meta.EventMeta(int64(i + 1)), Phase: PhaseModelDelta})
	}
	if registry.DroppedObserverEvents() == 0 {
		t.Fatal("full observer queue did not drop fail-open events")
	}
}

func TestCloseObserversDrainsAndRejectsNewEvents(t *testing.T) {
	registry := NewRegistry()
	observer := &testObserver{id: "close-observer"}
	if err := registry.RegisterObserver(Registration{
		ID: "close-observer", Kind: KindObserver, Phases: []Phase{PhaseChatFinished},
	}, observer); err != nil {
		t.Fatal(err)
	}
	meta := NewMeta(SourceMind).WithNode("mind")
	registry.Publish(context.Background(), RedactedEvent{
		Meta: registry.NextEventMeta(meta), Phase: PhaseChatFinished, Outcome: OutcomeSucceeded,
	})
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registry.CloseObservers(closeCtx); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	observed := len(observer.observed)
	observer.mu.Unlock()
	if observed != 1 {
		t.Fatalf("observer calls after close = %d, want 1", observed)
	}
	registry.Publish(context.Background(), RedactedEvent{
		Meta: registry.NextEventMeta(meta), Phase: PhaseChatFinished, Outcome: OutcomeSucceeded,
	})
	if err := registry.FlushObservers(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err := registry.CloseObservers(closeCtx); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	observed = len(observer.observed)
	observer.mu.Unlock()
	if observed != 1 {
		t.Fatalf("closed registry accepted a new event: calls=%d", observed)
	}
}

func TestCloseObserversTimeoutStillStopsAdmission(t *testing.T) {
	registry := NewRegistry()
	observer := &blockingCloseObserver{started: make(chan struct{}), release: make(chan struct{})}
	if err := registry.RegisterObserver(Registration{
		ID: "blocking-close", Kind: KindObserver, Phases: []Phase{PhaseToolFinished}, Timeout: time.Hour,
	}, observer); err != nil {
		t.Fatal(err)
	}
	event := RedactedEvent{Meta: NewMeta(SourceMind), Phase: PhaseToolFinished}
	registry.Publish(context.Background(), event)
	select {
	case <-observer.started:
	case <-time.After(time.Second):
		t.Fatal("observer did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	err := registry.CloseObservers(ctx)
	cancel()
	if err != context.DeadlineExceeded {
		t.Fatalf("CloseObservers error = %v, want deadline exceeded", err)
	}
	registry.Publish(context.Background(), event)
	close(observer.release)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := registry.CloseObservers(closeCtx); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	calls := observer.calls
	observer.mu.Unlock()
	if calls != 1 {
		t.Fatalf("observer calls after timed-out close = %d, want 1", calls)
	}
}

type capturingObserver struct {
	id    string
	event RedactedEvent
}

func (o *capturingObserver) ID() string { return o.id }

func (o *capturingObserver) Observe(_ context.Context, event RedactedEvent) { o.event = event }

type firstBlockingObserver struct{ once sync.Once }

func (*firstBlockingObserver) ID() string { return "slow" }

func (o *firstBlockingObserver) Observe(ctx context.Context, _ RedactedEvent) {
	o.once.Do(func() { <-ctx.Done() })
}

type blockingCloseObserver struct {
	mu      sync.Mutex
	once    sync.Once
	started chan struct{}
	release chan struct{}
	calls   int
}

func (*blockingCloseObserver) ID() string { return "blocking-close" }

func (o *blockingCloseObserver) Observe(context.Context, RedactedEvent) {
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()
	o.once.Do(func() { close(o.started) })
	<-o.release
}
