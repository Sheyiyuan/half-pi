package repl

import (
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
)

func (r *Repl) handleCommand(input string) bool {
	switch {
	case input == "/debug":
		r.core.Debug = !r.core.Debug
		r.emit(events.LevelInfo, events.TypeSystem, fmt.Sprintf("debug mode: %v", r.core.Debug))
		return true

	case input == "/mode":
		r.emit(events.LevelInfo, events.TypeSystem, fmt.Sprintf("current mode: %s", r.core.Mode))
		return true

	case strings.HasPrefix(input, "/mode "):
		mode := strings.TrimSpace(strings.TrimPrefix(input, "/mode "))
		switch mode {
		case "strict", "normal", "trust", "yolo":
			r.core.SetMode(mode)
			r.emit(events.LevelInfo, events.TypeModeChange, fmt.Sprintf("mode switched to: %s", mode))
		default:
			r.emit(events.LevelWarn, events.TypeSystem, fmt.Sprintf("unknown mode: %s (strict/normal/trust/yolo)", mode))
		}
		return true

	case input == "/session":
		r.handleSessionList()
		return true

	case strings.HasPrefix(input, "/session "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/session "))
		if strings.HasPrefix(arg, "name ") {
			r.handleSessionRename(arg)
		} else {
			r.handleSessionSwitch(arg)
		}
		return true
	}
	return false
}

func (r *Repl) handleSessionList() {
	sessions, err := r.store.ListSessions(r.groupID)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("list sessions: %v", err))
		return
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions in this group.")
		return
	}
	for _, sess := range sessions {
		count, _ := r.store.GetMessageCount(sess.ID)
		marker := " "
		if sess.ID == r.core.SessionID() {
			marker = "*"
		}
		name := sess.Name
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Printf(" %s %s  %s  %d msgs  %s\n", marker, shortID(sess.ID), sess.CreatedAt.Format("01-02 15:04"), count, name)
	}
}

func (r *Repl) handleSessionRename(arg string) {
	newName := strings.TrimSpace(strings.TrimPrefix(arg, "name "))
	if err := r.store.UpdateSessionName(r.core.SessionID(), newName); err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("rename: %v", err))
	} else {
		fmt.Printf("session renamed: %s\n", newName)
	}
}

func (r *Repl) handleSessionSwitch(targetPrefix string) {
	targetPrefix = strings.TrimSpace(targetPrefix)
	if targetPrefix == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /session <prefix>")
		return
	}
	sessions, err := r.store.FindSessionsByPrefix(r.groupID, targetPrefix)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("find session: %v", err))
		return
	}
	if len(sessions) == 0 {
		fmt.Printf("no session matched prefix %q\n", targetPrefix)
		return
	}
	if len(sessions) > 1 {
		fmt.Printf("ambiguous prefix %q, matched %d sessions:\n", targetPrefix, len(sessions))
		for _, s := range sessions {
			count, _ := r.store.GetMessageCount(s.ID)
			fmt.Printf("  %s  %s  %d msgs  %s\n", s.ID, s.CreatedAt.Format("01-02 15:04"), count, s.Name)
		}
		fmt.Println("use a longer prefix")
		return
	}
	targetID := sessions[0].ID
	if err := r.core.SaveSession(); err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("save session: %v", err))
	}
	if err := r.core.SetStore(r.store, targetID); err != nil {
		fmt.Printf("session not found: %s\n", targetID)
		return
	}
	fmt.Printf("switched to session %s\n", shortID(targetID))
}

func (r *Repl) emit(level, typ, msg string) {
	r.bus.PublishSync(events.New("", "repl", level, typ, msg))
}

func shortID(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 12 {
		s = s[:12]
	}
	return s
}
