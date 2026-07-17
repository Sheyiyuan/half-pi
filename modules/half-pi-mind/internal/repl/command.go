package repl

import (
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
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

	case input == "/hand":
		r.handleHandList()
		return true

	case strings.HasPrefix(input, "/hand add "):
		label := strings.TrimSpace(strings.TrimPrefix(input, "/hand add "))
		r.handleHandAdd(label)
		return true

	case strings.HasPrefix(input, "/hand remove "):
		idStr := strings.TrimSpace(strings.TrimPrefix(input, "/hand remove "))
		r.handleHandRemove(idStr)
		return true

	case input == "/peers":
		r.handlePeers()
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

func (r *Repl) handleHandList() {
	tokens, err := r.store.ListHandTokens()
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("list hand tokens: %v", err))
		return
	}
	if len(tokens) == 0 {
		fmt.Println("No hand tokens. Use /hand add <label> to create one.")
		return
	}
	fmt.Println("id  label          token                              created")
	for _, ht := range tokens {
		fmt.Printf("%2d  %-14s %s  %s\n", ht.ID, ht.Label, ht.Token, ht.CreatedAt.Format("01-02 15:04"))
	}
}

func (r *Repl) handleHandAdd(label string) {
	if label == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand add <label>")
		return
	}
	ht, err := r.store.AddHandToken(label)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("add hand token: %v", err))
		return
	}
	fmt.Printf("Hand created:\n")
	fmt.Printf("  id:    %d\n", ht.ID)
	fmt.Printf("  label: %s\n", ht.Label)
	fmt.Printf("  token: %s\n", ht.Token)
	fmt.Println()
	fmt.Printf("Hand 配置参考:\n")
	fmt.Printf("  [server]\n")
	fmt.Printf("  url = \"ws://127.0.0.1:15707/ws\"\n")
	fmt.Printf("  token = \"%s\"\n", ht.Token)
}

func (r *Repl) handleHandRemove(idStr string) {
	var id int64
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand remove <id>")
		return
	}
	if err := r.store.RemoveHandToken(id); err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("remove hand token: %v", err))
		return
	}
	fmt.Printf("Hand token %d removed\n", id)
}

func (r *Repl) handlePeers() {
	if r.hub == nil {
		fmt.Println("hub not running")
		return
	}
	peers := r.hub.Peers()
	if len(peers) == 0 {
		fmt.Println("No connected peers.")
		return
	}
	for _, id := range peers {
		fmt.Printf("  %s\n", id)
	}
}
