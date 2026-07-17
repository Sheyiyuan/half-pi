package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func main() {
	var (
		showVersion bool
		replMode    bool
	)
	flag.BoolVar(&showVersion, "version", false, "打印版本号")
	flag.BoolVar(&replMode, "repl", false, "交互式 REPL 模式")
	flag.Parse()

	if showVersion {
		fmt.Println("half-pi-mind version dev")
		return
	}

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

	db, err := store.New(env.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	bus := events.NewEventBus()
	defer bus.Close()

	if replMode {
		bus.Subscribe(events.NewConsoleWriter())
	} else {
		logPath := filepath.Join(env.LogDir, "mind.log")
		fw, err := events.NewFileWriter(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "log file open failed: %v\n", err)
			bus.Subscribe(events.NewConsoleWriter())
		} else {
			bus.Subscribe(fw)
		}
	}

	wsHub := hub.New()
	wsHub.OnHandshake(func(peer *hub.Peer, msg protocol.Envelope) error {
		reg, err := protocol.DecodePayload[protocol.Register](&msg)
		if err != nil {
			return err
		}
		if reg.Token == "" {
			return fmt.Errorf("token is required")
		}
		ht, err := db.ValidateHandToken(reg.Token)
		if err != nil {
			return fmt.Errorf("invalid token")
		}
		bus.Publish(events.New("", "hub", events.LevelInfo, events.TypeSystem,
			fmt.Sprintf("[HUB] %s (%s) 已连接", peer.ID, ht.Label)))
		return nil
	})
	wsHub.OnDisconnect(func(peer *hub.Peer) {
		bus.Publish(events.New("", "hub", events.LevelInfo, events.TypeSystem,
			fmt.Sprintf("[HUB] %s 已断开", peer.ID)))
	})
	wsHub.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeHandEvent {
			evt, _ := protocol.DecodePayload[protocol.HandEvent](&msg)
			level := events.LevelInfo
			if evt.Status == "triggered" {
				level = events.LevelWarn
			}
			bus.Publish(events.New("", peer.ID, level, events.TypeSystem,
				fmt.Sprintf("[%s/%s] %s\n%s", peer.ID, evt.Name, evt.Status, evt.Output)))
		}
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

	if replMode {
		runREPL(env, cfg, db, bus, wsHub)
	} else {
		runService(env, bus)
	}
}
