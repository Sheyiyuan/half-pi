package repl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func (r *Repl) handleCommand(input string) bool {
	switch {
	case input == "/debug":
		r.emit(events.LevelInfo, events.TypeSystem, fmt.Sprintf("debug mode: %v", r.core.ToggleDebug()))
		return true

	case input == "/mode":
		r.emit(events.LevelInfo, events.TypeSystem, fmt.Sprintf("current mode: %s", r.core.SecurityMode()))
		return true

	case strings.HasPrefix(input, "/mode "):
		mode := strings.TrimSpace(strings.TrimPrefix(input, "/mode "))
		switch mode {
		case "strict", "normal", "trust", "yolo":
			if err := r.core.SetMode(mode); err != nil {
				r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("switch mode: %v", err))
			} else {
				r.emit(events.LevelInfo, events.TypeModeChange, fmt.Sprintf("mode switched to: %s", mode))
			}
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

	case input == "/hand" || input == "/hand list":
		r.handleHandList()
		return true

	case input == "/face list":
		r.handleFaceList()
		return true

	case strings.HasPrefix(input, "/face add "):
		r.handleFaceAdd(strings.TrimSpace(strings.TrimPrefix(input, "/face add ")))
		return true

	case strings.HasPrefix(input, "/face remove "):
		r.handleCredentialRemove(hub.PeerFace, strings.TrimSpace(strings.TrimPrefix(input, "/face remove ")))
		return true

	case strings.HasPrefix(input, "/hand select "):
		r.handleHandSelect(strings.TrimSpace(strings.TrimPrefix(input, "/hand select ")))
		return true

	case input == "/hand online":
		r.handleHandOnline()
		return true

	case strings.HasPrefix(input, "/hand info "):
		r.handleHandInfo(strings.TrimSpace(strings.TrimPrefix(input, "/hand info ")))
		return true

	case strings.HasPrefix(input, "/hand exec "):
		r.handleHandExec(strings.TrimSpace(strings.TrimPrefix(input, "/hand exec ")))
		return true

	case strings.HasPrefix(input, "/hand cancel "):
		r.handleHandCancel(strings.TrimSpace(strings.TrimPrefix(input, "/hand cancel ")))
		return true

	case strings.HasPrefix(input, "/hand run "):
		r.handleHandRun(strings.TrimSpace(strings.TrimPrefix(input, "/hand run ")))
		return true

	case strings.HasPrefix(input, "/hand task "):
		r.handleHandTask(strings.TrimSpace(strings.TrimPrefix(input, "/hand task ")))
		return true

	case strings.HasPrefix(input, "/hand add "):
		label := strings.TrimSpace(strings.TrimPrefix(input, "/hand add "))
		r.handleHandAdd(label)
		return true

	case strings.HasPrefix(input, "/hand remove "):
		r.handleCredentialRemove(hub.PeerHand, strings.TrimSpace(strings.TrimPrefix(input, "/hand remove ")))
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
	actor, err := r.switchActor(targetID)
	if err != nil {
		fmt.Printf("session not found: %s\n", targetID)
		return
	}
	r.actor, r.core, r.bridge = actor, actor.Core(), actor.Bridge()
	fmt.Printf("switched to session %s\n", shortID(targetID))
}

func (r *Repl) emit(level, typ, msg string) {
	r.emitForSession(r.core.SessionID(), level, typ, msg)
}

func (r *Repl) emitForSession(sessionID, level, typ, msg string) {
	r.bus.PublishSync(events.New(sessionID, "repl", level, typ, msg))
}

func shortID(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 12 {
		s = s[:12]
	}
	return s
}

func (r *Repl) handleHandList() {
	credentials, err := r.store.ListHandCredentials()
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("list hand tokens: %v", err))
		return
	}
	if len(credentials) == 0 {
		fmt.Println("No Hand credentials. Use /hand add <label> to create one.")
		return
	}
	fmt.Println("id  hand           created")
	for _, credential := range credentials {
		fmt.Printf("%2d  %-14s %s\n", credential.ID, credential.Label, credential.CreatedAt.Format("01-02 15:04"))
	}
}

func (r *Repl) handleHandAdd(label string) {
	if label == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand add <label>")
		return
	}
	credential, err := r.store.AddHandCredential(label)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("add hand token: %v", err))
		return
	}
	fmt.Printf("Hand created:\n")
	fmt.Printf("  id:              %d\n", credential.ID)
	fmt.Printf("  label:           %s\n", credential.Label)
	fmt.Printf("  token:           %s\n", credential.Token)
	fmt.Printf("  application_key: %s\n", credential.ApplicationKey)
	fmt.Println()
	fmt.Printf("Hand 配置参考:\n")
	fmt.Printf("  [server]\n")
	fmt.Printf("  url = \"ws://127.0.0.1:15707/ws\"\n")
	fmt.Printf("  token = \"%s\"\n", credential.Token)
	fmt.Printf("  application_key = \"%s\"\n", credential.ApplicationKey)
}

func (r *Repl) handleFaceList() {
	credentials, err := r.store.ListFaceTokens()
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("list Face credentials: %v", err))
		return
	}
	if len(credentials) == 0 {
		fmt.Println("No Face credentials. Use /face add <label> --scopes <scopes> to create one.")
		return
	}
	fmt.Println("id  face           scopes  created")
	for _, credential := range credentials {
		fmt.Printf("%2d  %-14s %s  %s\n", credential.ID, credential.Label, joinScopes(credential.Scopes), credential.CreatedAt.Format("01-02 15:04"))
	}
}

func (r *Repl) handleFaceAdd(args string) {
	fields := strings.Fields(args)
	if len(fields) != 3 || fields[1] != "--scopes" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /face add <label> --scopes <comma-separated-scopes>")
		return
	}
	rawScopes := strings.Split(fields[2], ",")
	scopes := make([]protocol.FaceScope, len(rawScopes))
	for i, scope := range rawScopes {
		scopes[i] = protocol.FaceScope(scope)
	}
	credential, err := r.store.AddFaceToken(fields[0], scopes)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("add Face credential: %v", err))
		return
	}
	fmt.Println("Face created:")
	fmt.Printf("  id:              %d\n", credential.ID)
	fmt.Printf("  label:           %s\n", credential.Label)
	fmt.Printf("  token:           %s\n", credential.Token)
	fmt.Printf("  application_key: %s\n", credential.ApplicationKey)
	fmt.Printf("  scopes:          %s\n", joinScopes(credential.Scopes))
}

func (r *Repl) handleCredentialRemove(peerType hub.PeerType, args string) {
	fields := strings.Fields(args)
	if len(fields) != 2 || (fields[0] != "--id" && fields[0] != "--label") || fields[1] == "" {
		r.emit(events.LevelWarn, events.TypeSystem, fmt.Sprintf("usage: /%s remove --id <id> | --label <label>", peerType))
		return
	}
	label, err := r.removeCredential(peerType, fields[0], fields[1])
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("remove %s credential: %v", peerType, err))
		return
	}
	if r.hub != nil {
		r.hub.RemoveByType(peerType, label)
	}
	fmt.Printf("%s credential %q removed\n", peerType, label)
}

func (r *Repl) removeCredential(peerType hub.PeerType, selector, value string) (string, error) {
	if peerType == hub.PeerHand {
		credentials, err := r.store.ListHandCredentials()
		if err != nil {
			return "", err
		}
		label, id, err := selectCredential(credentials, selector, value)
		if err != nil {
			return "", err
		}
		if selector == "--id" {
			err = r.store.RemoveHandCredential(id)
		} else {
			err = r.store.RemoveHandCredentialByLabel(label)
		}
		return label, err
	}
	credentials, err := r.store.ListFaceTokens()
	if err != nil {
		return "", err
	}
	plain := make([]store.Credential, len(credentials))
	for i := range credentials {
		plain[i] = credentials[i].Credential
	}
	label, id, err := selectCredential(plain, selector, value)
	if err != nil {
		return "", err
	}
	if selector == "--id" {
		err = r.store.RemoveFaceToken(id)
	} else {
		err = r.store.RemoveFaceTokenByLabel(label)
	}
	return label, err
}

func selectCredential(credentials []store.Credential, selector, value string) (string, int64, error) {
	if selector == "--label" {
		for _, credential := range credentials {
			if credential.Label == value {
				return credential.Label, credential.ID, nil
			}
		}
		return "", 0, fmt.Errorf("credential not found")
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, fmt.Errorf("invalid credential ID")
	}
	for _, credential := range credentials {
		if credential.ID == id {
			return credential.Label, id, nil
		}
	}
	return "", 0, fmt.Errorf("credential not found")
}

func joinScopes(scopes []protocol.FaceScope) string {
	values := make([]string, len(scopes))
	for i, scope := range scopes {
		values[i] = string(scope)
	}
	return strings.Join(values, ",")
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
	for _, peer := range peers {
		fmt.Printf("  %s/%s\n", peer.Type, peer.Label)
	}
}
