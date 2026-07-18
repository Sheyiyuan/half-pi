package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/dispatcher"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/facegateway"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
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
	if count, err := db.LegacyHandTokenCount(); err != nil {
		fmt.Fprintf(os.Stderr, "legacy Hand credential check failed: %v\n", err)
		os.Exit(1)
	} else if count > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d legacy Hand credentials are disabled; recreate them with /hand add\n", count)
	}
	if recovered, err := db.RecoverRemoteRuns(); err != nil {
		fmt.Fprintf(os.Stderr, "remote run recovery failed: %v\n", err)
		os.Exit(1)
	} else if recovered > 0 {
		fmt.Fprintf(os.Stderr, "recovered %d unfinished remote runs\n", recovered)
	}
	if recovered, err := db.RecoverRemoteTasks(); err != nil {
		fmt.Fprintf(os.Stderr, "remote task recovery failed: %v\n", err)
		os.Exit(1)
	} else if recovered > 0 {
		fmt.Fprintf(os.Stderr, "marked %d unfinished remote tasks stale\n", recovered)
	}

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
	authority := remoteexec.NewAuthority(wsHub, remoteexec.NewRegistry(db), bus)
	taskService := remoteexec.NewTaskService(authority, db)
	conversations, err := newConversationManager(env, cfg, db, bus, authority, taskService)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conversation runtime init failed: %v\n", err)
		os.Exit(1)
	}
	faceGateway, err := facegateway.New(facegateway.Config{
		Hub: wsHub, Store: db, Conversations: conversations, Authority: authority, Tasks: taskService,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Face Gateway init failed: %v\n", err)
		os.Exit(1)
	}
	dispatcher.Install(wsHub, db, authority, faceGateway)

	var httpServer *http.Server
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
		httpServer = &http.Server{Addr: addr, Handler: mux}
		go func() {
			fmt.Fprintf(os.Stderr, "WS Hub listening on %s/ws\n", addr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "hub server: %v\n", err)
			}
		}()
	}

	if replMode {
		runREPL(conversations, bus, db, cfg.Server.Enabled, authority.Hub)
	} else {
		runService(env, bus)
	}
	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = httpServer.Shutdown(shutdownCtx)
		cancel()
	}
	if err := authority.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "remote execution shutdown: %v\n", err)
	}
}
