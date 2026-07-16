package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/repl"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func main() {
	env, err := setup.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(env.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}

	modelID := cfg.LLM.DefaultModel
	if modelID == "" && len(cfg.LLM.Models) > 0 {
		modelID = cfg.LLM.Models[0].ID
	}
	rm, err := cfg.ResolveModel(modelID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model resolve error: %v\n", err)
		os.Exit(1)
	}

	provider := llm.NewOpenAI(rm.Endpoint, rm.APIKey, rm.Name)
	exec := local.New()

	skillStore, err := skill.LoadFromDir(env.SkillsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill load failed: %v\n", err)
	}
	local.SetSkillStore(skillStore)

	db, err := store.New(env.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	cwd, _ := os.Getwd()
	group, err := db.UpsertGroup(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "group init failed: %v\n", err)
		os.Exit(1)
	}

	sessionID := uuid.Must(uuid.NewV7()).String()
	if err := db.CreateSession(group.ID, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "session create failed: %v\n", err)
		os.Exit(1)
	}

	bus := events.NewEventBus()
	defer bus.Close()
	bus.Subscribe(events.NewConsoleWriter())

	core, err := agentcore.New(provider, exec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "core init failed: %v\n", err)
		os.Exit(1)
	}
	core.Bus = bus
	core.SetSkills(skillStore)
	if err := core.SetStore(db, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "session load failed: %v\n", err)
		os.Exit(1)
	}

	wsHub := hub.New()
	core.SetHub(wsHub)

	local.SetRemoteBridge(&local.RemoteBridge{
		Hub:             wsHub,
		ActiveHand:      core.ActiveHand,
		SetActiveHand:   core.SetActiveHand,
		PendingCall:     core.PendingCall,
		CheckAndConfirm: core.CheckAndConfirm,
	})

	if cfg.Server.Enabled {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		mux := http.NewServeMux()
		mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			wsHub.ServeWS(conn)
		})
		go func() {
			fmt.Fprintf(os.Stderr, "WS Hub listening on %s/ws\n", addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				fmt.Fprintf(os.Stderr, "hub server: %v\n", err)
			}
		}()
	}

	repl.Run(core, bus, db, group.ID, cfg.Server.Enabled)
}
