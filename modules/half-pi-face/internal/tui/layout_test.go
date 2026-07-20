package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestResponsiveLayouts(t *testing.T) {
	tests := []struct {
		width  int
		height int
		mode   layoutMode
		short  bool
	}{
		{width: 160, height: 45, mode: layoutWide},
		{width: 120, height: 30, mode: layoutStandard},
		{width: 80, height: 24, mode: layoutCompact},
		{width: 50, height: 16, mode: layoutCompact, short: true},
	}
	for _, test := range tests {
		layout := calculateLayout(test.width, test.height)
		if layout.TooSmall || layout.Mode != test.mode || layout.Short != test.short {
			t.Fatalf("layout %dx%d = %+v", test.width, test.height, layout)
		}
		for name, area := range map[string]rect{
			"header": layout.Header, "chat": layout.Chat, "composer": layout.Composer, "footer": layout.Footer,
		} {
			if area.X < 0 || area.Y < 0 || area.X+area.W > test.width || area.Y+area.H > test.height || area.W <= 0 || area.H <= 0 {
				t.Fatalf("%dx%d %s outside viewport: %+v", test.width, test.height, name, area)
			}
		}
		if overlaps(layout.Chat, layout.Composer) || overlaps(layout.Header, layout.Chat) || overlaps(layout.Footer, layout.Composer) {
			t.Fatalf("layout %dx%d overlaps: %+v", test.width, test.height, layout)
		}
		if layout.Chat.W < 40 {
			t.Fatalf("layout %dx%d chat width = %d", test.width, test.height, layout.Chat.W)
		}
	}
}

func TestViewKeepsStableTerminalRectangleWithUnicode(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft.Summary.Name = "这是一个很长的中文对话标题 with emoji \U0001F642"
	model.composer.SetValue("组合字符 e\u0301 and 中文")
	model.resize(80, 24)
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 24 {
		t.Fatalf("view height = %d, want 24", len(lines))
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width != 80 {
			t.Fatalf("line %d width = %d, want 80: %q", index, width, line)
		}
	}
}

func overlaps(left, right rect) bool {
	return left.X < right.X+right.W && left.X+left.W > right.X && left.Y < right.Y+right.H && left.Y+left.H > right.Y
}
