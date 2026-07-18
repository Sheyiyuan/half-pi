package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/dispatcher"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/facegateway"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Mind exited: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output, logs io.Writer) (runErr error) {
	var (
		showVersion bool
		replMode    bool
	)
	flags := flag.NewFlagSet("half-pi-mind", flag.ContinueOnError)
	flags.SetOutput(logs)
	flags.BoolVar(&showVersion, "version", false, "打印版本号")
	flags.BoolVar(&replMode, "repl", false, "交互式 REPL 模式")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if showVersion {
		_, err := fmt.Fprintln(output, "half-pi-mind version dev")
		return err
	}

	env, err := setup.Init()
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	cfg, err := config.Load(env.Config)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := store.New(env.DBPath)
	if err != nil {
		return fmt.Errorf("initialize store: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close store: %w", err))
		}
	}()
	if count, err := db.LegacyHandTokenCount(); err != nil {
		return fmt.Errorf("check legacy Hand credentials: %w", err)
	} else if count > 0 {
		fmt.Fprintf(logs, "warning: %d legacy Hand credentials are disabled; recreate them with /hand add\n", count)
	}
	if recovered, err := db.RecoverRemoteRuns(); err != nil {
		return fmt.Errorf("recover remote runs: %w", err)
	} else if recovered > 0 {
		fmt.Fprintf(logs, "recovered %d unfinished remote runs\n", recovered)
	}
	if recovered, err := db.RecoverRemoteTasks(); err != nil {
		return fmt.Errorf("recover remote tasks: %w", err)
	} else if recovered > 0 {
		fmt.Fprintf(logs, "marked %d unfinished remote tasks stale\n", recovered)
	}
	if recovered, err := db.RecoverApprovals(time.Now().UTC()); err != nil {
		return fmt.Errorf("recover approvals: %w", err)
	} else if recovered > 0 {
		fmt.Fprintf(logs, "cancelled %d unfinished approvals\n", recovered)
	}

	bus := events.NewEventBus()
	defer bus.Close()

	if replMode {
		bus.Subscribe(events.NewConsoleWriter())
	} else {
		logPath := filepath.Join(env.LogDir, "mind.log")
		fw, err := events.NewFileWriter(logPath)
		if err != nil {
			fmt.Fprintf(logs, "log file open failed: %v\n", err)
			bus.Subscribe(events.NewConsoleWriter())
		} else {
			bus.Subscribe(fw)
		}
	}

	wsHub := hub.New()
	authority := remoteexec.NewAuthority(wsHub, remoteexec.NewRegistry(db), bus)
	defer func() {
		if err := authority.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close remote execution: %w", err))
		}
	}()
	taskService := remoteexec.NewTaskService(authority, db)
	approvalBroker, err := approval.New(approval.Config{Auditor: db})
	if err != nil {
		return fmt.Errorf("initialize approval broker: %w", err)
	}
	defer func() {
		if err := approvalBroker.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close approval broker: %w", err))
		}
	}()
	conversations, err := newConversationManager(env, cfg, db, bus, approvalBroker, authority, taskService)
	if err != nil {
		return fmt.Errorf("initialize conversation runtime: %w", err)
	}
	faceGateway, err := facegateway.New(facegateway.Config{
		Hub: wsHub, Store: db, Conversations: conversations, Approvals: approvalBroker,
		Authority: authority, Tasks: taskService,
	})
	if err != nil {
		return fmt.Errorf("initialize Face Gateway: %w", err)
	}
	dispatcher.Install(wsHub, db, authority, faceGateway)

	var hubServer *runningHubServer
	if cfg.Server.Enabled {
		hubServer, err = startHubServer(cfg.Server, wsHub)
		if err != nil {
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := hubServer.shutdown(shutdownCtx); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("shutdown Hub server: %w", err))
			}
		}()
		fmt.Fprintf(logs, "WS Hub listening on %s\n", hubServer.wsURL)
		if !replMode {
			if err := writeMindReady(output, hubServer.ready(os.Getpid())); err != nil {
				return err
			}
		}
	}

	if replMode {
		return runREPL(conversations, approvalBroker, bus, db, cfg.Server.Enabled, authority.Hub)
	} else {
		var serverErrors <-chan error
		if hubServer != nil {
			serverErrors = hubServer.errors
		}
		return runService(env, bus, serverErrors)
	}
}
