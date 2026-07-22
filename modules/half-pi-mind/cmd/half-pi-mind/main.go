package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/adminipc"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/dispatcher"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/facegateway"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/statelock"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(writeRunError(os.Args[1:], os.Stderr, err))
	}
}

func writeRunError(args []string, output io.Writer, err error) int {
	var cliErr *cliError
	if errors.As(err, &cliErr) {
		if isManagementCommand(args) && wantsJSONOutput(args) {
			response := struct {
				OK    bool `json:"ok"`
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}{OK: false}
			response.Error.Code = cliErr.code
			response.Error.Message = cliErr.msg
			_ = json.NewEncoder(output).Encode(response)
		} else {
			fmt.Fprintf(output, "%s: %s\n", cliErr.code, cliErr.msg)
		}
		return cliErr.exit
	}
	fmt.Fprintf(output, "Mind exited: %v\n", err)
	return 1
}

func wantsJSONOutput(args []string) bool {
	for i, arg := range args {
		if arg == "--format=json" {
			return true
		}
		if arg == "--format" && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}

func run(args []string, output, logs io.Writer) (runErr error) {
	if isManagementCommand(args) {
		return runCLI(args, output, logs)
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return usage("unknown command %q", args[0])
	}
	var (
		showVersion bool
		replMode    bool
	)
	flags := flag.NewFlagSet("half-pi-mind", flag.ContinueOnError)
	flags.SetOutput(logs)
	flags.BoolVar(&showVersion, "version", false, "打印版本号")
	flags.BoolVar(&replMode, "repl", false, "交互式 REPL 模式")
	if err := flags.Parse(args); err != nil {
		return usage("%v", err)
	}
	if flags.NArg() > 0 {
		return usage("unknown command %q", flags.Arg(0))
	}

	if showVersion {
		_, err := fmt.Fprintln(output, "half-pi-mind version dev")
		return err
	}

	env, err := setup.Init()
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	startedAt := time.Now().UTC()
	mode := "service"
	if replMode {
		mode = "repl"
	}
	lockCtx, cancelLock := context.WithTimeout(context.Background(), 2*time.Second)
	stateLock, err := statelock.Acquire(lockCtx, env.LockPath, statelock.Info{PID: os.Getpid(), Mode: mode, StartedAt: startedAt})
	cancelLock()
	if err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	defer func() {
		if err := stateLock.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("release state lock: %w", err))
		}
	}()

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
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conversations.Close(closeCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close conversation lifecycle: %w", err))
		}
	}()
	faceGateway, err := facegateway.New(facegateway.Config{
		Hub: wsHub, Store: db, Conversations: conversations, Approvals: approvalBroker,
		Authority: authority, Tasks: taskService,
	})
	if err != nil {
		return fmt.Errorf("initialize Face Gateway: %w", err)
	}
	dispatcher.Install(wsHub, db, authority, faceGateway)
	managementRuntime := management.Runtime{
		PID: os.Getpid(), StartedAt: startedAt, Mode: mode, HubEnabled: cfg.Server.Enabled,
	}
	managementService := management.New(db, wsHub, managementRuntime)
	adminServer, err := adminipc.Start(env.ControlEndpoint, managementService)
	if err != nil {
		return fmt.Errorf("start management IPC: %w", err)
	}

	var hubServer *runningHubServer
	wsURL := ""
	if cfg.Server.Enabled {
		hubServer, err = startHubServer(cfg.Server, wsHub)
		if err != nil {
			if closeErr := adminServer.Close(); closeErr != nil {
				return errors.Join(err, fmt.Errorf("close management IPC: %w", closeErr))
			}
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := hubServer.shutdown(shutdownCtx); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("shutdown Hub server: %w", err))
			}
		}()
		wsURL = hubServer.wsURL
		fmt.Fprintf(logs, "WS Hub listening on %s\n", hubServer.wsURL)
	}
	managementRuntime.WSURL = wsURL
	managementService.UpdateRuntime(managementRuntime)
	defer func() {
		if err := adminServer.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close management IPC: %w", err))
		}
	}()
	if !replMode && hubServer != nil {
		if err := writeMindReady(output, hubServer.ready(os.Getpid())); err != nil {
			return err
		}
	}

	if replMode {
		return runREPL(conversations, approvalBroker, bus, db, cfg.Server.Enabled, authority.Hub, managementService)
	} else {
		var serverErrors <-chan error
		if hubServer != nil {
			serverErrors = hubServer.errors
		}
		return runService(env, bus, serverErrors)
	}
}
