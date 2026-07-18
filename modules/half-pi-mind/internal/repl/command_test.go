package repl

import (
	"path/filepath"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestHandListCommandKeepsLegacyAlias(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "repl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &Repl{store: db}

	if !r.handleCommand("/hand") {
		t.Fatal("legacy /hand command was not handled")
	}
	if !r.handleCommand("/hand list") {
		t.Fatal("/hand list command was not handled")
	}
}
