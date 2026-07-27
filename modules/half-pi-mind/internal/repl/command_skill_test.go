package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

func capturedMessages(w *captureWriter) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var buf strings.Builder
	for _, event := range w.events {
		buf.WriteString(event.Message)
		buf.WriteString("\n")
	}
	return buf.String()
}

func newSkillRepl(t *testing.T, dir string) (*Repl, *captureWriter, *skill.Store) {
	t.Helper()
	store, err := skill.LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	core, err := agentcore.New(nil, local.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	core.SetSkills(store)
	writer := &captureWriter{}
	bus := events.NewEventBus()
	bus.Subscribe(writer)
	core.Bus = bus
	return &Repl{core: core, bus: bus}, writer, store
}

func TestSkillCommandIsRouted(t *testing.T) {
	dir := t.TempDir()
	r, _, _ := newSkillRepl(t, dir)
	for _, input := range []string{"/skill", "/skill list", "/skill reload", "/skill warnings", "/skill bogus"} {
		if !r.handleCommand(input) {
			t.Fatalf("%q was not handled as a REPL command", input)
		}
	}
}

func TestSkillReloadPicksUpNewFiles(t *testing.T) {
	dir := t.TempDir()
	r, writer, store := newSkillRepl(t, dir)
	if len(store.SummariesForGroup("")) != 0 {
		t.Fatal("expected an empty store before reload")
	}

	if err := os.WriteFile(filepath.Join(dir, "added.skill.md"),
		[]byte("---\nname: added\ndescription: Added after startup\n---\nBody."), 0o600); err != nil {
		t.Fatal(err)
	}
	r.handleCommand("/skill reload")

	summaries := store.SummariesForGroup("")
	if len(summaries) != 1 || summaries[0].Name != "added" {
		t.Fatalf("reload did not pick up the new skill: %+v", summaries)
	}
	if !strings.Contains(capturedMessages(writer), "skills reloaded: 1 visible in this group (was 0)") {
		t.Fatalf("reload did not report the count change: %q", capturedMessages(writer))
	}
}

func TestSkillReloadChangesSnapshotDigestForEnvironmentCheck(t *testing.T) {
	dir := t.TempDir()
	r, _, store := newSkillRepl(t, dir)
	before := store.Snapshot()

	if err := os.WriteFile(filepath.Join(dir, "added.skill.md"),
		[]byte("---\nname: added\ndescription: Added\n---\nBody."), 0o600); err != nil {
		t.Fatal(err)
	}
	r.handleCommand("/skill reload")

	after := store.Snapshot()
	// revision/digest 必须变化，模型请求 admission 才会对旧环境 fail closed。
	if after.Revision <= before.Revision || after.Digest == before.Digest {
		t.Fatalf("reload did not advance the environment token: before=%+v after=%+v", before, after)
	}
}

func TestSkillWarningsAreVisible(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.skill.md"), []byte("no frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, writer, _ := newSkillRepl(t, dir)

	r.handleCommand("/skill warnings")
	if !strings.Contains(capturedMessages(writer), "broken.skill.md") {
		t.Fatalf("skill warnings were not surfaced: %q", capturedMessages(writer))
	}
}

func TestSkillCommandsTolerateMissingStore(t *testing.T) {
	core, err := agentcore.New(nil, local.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	writer := &captureWriter{}
	bus := events.NewEventBus()
	bus.Subscribe(writer)
	core.Bus = bus
	r := &Repl{core: core, bus: bus}

	for _, input := range []string{"/skill list", "/skill reload", "/skill warnings"} {
		if !r.handleCommand(input) {
			t.Fatalf("%q was not handled", input)
		}
	}
	if !strings.Contains(capturedMessages(writer), "skill system is not initialized") {
		t.Fatalf("missing store was not reported: %q", capturedMessages(writer))
	}
}
