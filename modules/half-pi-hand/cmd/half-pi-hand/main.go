// Half-Pi Hand — 远程执行节点。
// 通过 WebSocket 连接 Mind，接收 RPC 工具执行请求并返回结果。
//
// 配置优先级（高到低）：CLI flag → 环境变量 → 配置文件
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/hand"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/taskmanager"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Hand exited: %v\n", err)
		os.Exit(1)
	}
}

type handReady struct {
	Type   string `json:"type"`
	PID    int    `json:"pid"`
	HandID string `json:"hand_id"`
}

func run(ctx context.Context, args []string, output, logs io.Writer) (runErr error) {
	if output == nil || logs == nil {
		return fmt.Errorf("Hand output streams are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	flags := flag.NewFlagSet("half-pi-hand", flag.ContinueOnError)
	flags.SetOutput(logs)
	cfgPath := flags.String("config", "", "配置文件路径（默认 ~/.half-pi/hand/config.toml）")
	server := flags.String("server", "", "Mind WebSocket 地址")
	token := flags.String("token", "", "认证令牌")
	applicationKey := flags.String("application-key", "", "应用密钥")
	id := flags.String("id", "", "Hand 唯一标识")
	showVersion := flags.Bool("version", false, "打印版本号")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(output, "half-pi-hand version dev")
		return err
	}

	path := *cfgPath
	if path == "" {
		path = config.DefaultPath()
		if err := config.WriteDefault(path); err != nil {
			return fmt.Errorf("create config: %w", err)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if *server != "" {
		cfg.Server.URL = *server
	}
	if *token != "" {
		cfg.Server.Token = *token
	}
	if *applicationKey != "" {
		cfg.Server.ApplicationKey = *applicationKey
	}
	if *id != "" {
		cfg.Hand.ID = *id
	}

	if cfg.Server.URL == "" {
		cfg.Server.URL = config.DefaultServerURL
	}
	if err := cfg.ValidateCredentials(); err != nil {
		return fmt.Errorf("validate Hand credentials: %w", err)
	}

	if cfg.Hand.WorkDir != "" {
		if err := os.Chdir(cfg.Hand.WorkDir); err != nil {
			return fmt.Errorf("change Hand working directory: %w", err)
		}
	}

	info := hand.CollectHandInfo()

	manager, err := taskmanager.New(taskmanager.Config{
		Dir: cfg.Hand.Tasks.Dir, MaxRunning: cfg.Hand.Tasks.MaxRunning,
		MaxRuntime: cfg.Hand.Tasks.MaxRuntimeDuration(), MaxLogBytes: cfg.Hand.Tasks.MaxLogBytes,
		Retention: cfg.Hand.Tasks.RetentionDuration(), MaxRetained: cfg.Hand.Tasks.MaxRetained,
	})
	if err != nil {
		return fmt.Errorf("initialize task manager: %w", err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close task manager: %w", err))
		}
	}()

	backoff := 1 * time.Second
	maxBackoff := time.Duration(cfg.Hand.Retry.MaxBackoff) * time.Second

	for {
		if ctx.Err() != nil {
			return nil
		}
		conn, err := wss.NewClient(cfg.Server.URL).ConnectAndRegister(wss.Credentials{
			Label: cfg.Hand.ID, Type: protocol.PeerHand, Token: cfg.Server.Token,
			ApplicationKey: cfg.Server.ApplicationKey, Info: info,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !shouldRetry(err) {
				return fmt.Errorf("permanent Hand handshake failure: %w", err)
			}
			fmt.Fprintf(logs, "连接失败: %v，%v 后重试\n", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}
			backoff = sleepBackoff(backoff, maxBackoff)
			continue
		}
		if err := writeHandReady(output, handReady{Type: "hand.ready", PID: os.Getpid(), HandID: cfg.Hand.ID}); err != nil {
			_ = conn.Conn.Close()
			return err
		}
		fmt.Fprintf(logs, "Hand %s 已连接\n", cfg.Hand.ID)
		backoff = 1 * time.Second

		h := hand.NewWithTaskManager(conn, cfg, manager)
		err = h.Serve(ctx)
		_ = conn.Conn.Close()
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			fmt.Fprintf(logs, "Hand 退出: %v，%v 后重连\n", err, backoff)
		} else {
			fmt.Fprintf(logs, "Hand 连接已断开，%v 后重连\n", backoff)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil
		}
		backoff = sleepBackoff(backoff, maxBackoff)
	}
}

func writeHandReady(output io.Writer, ready handReady) error {
	if output == nil {
		return fmt.Errorf("ready output is required")
	}
	if err := json.NewEncoder(output).Encode(ready); err != nil {
		return fmt.Errorf("write Hand ready message: %w", err)
	}
	return nil
}

func shouldRetry(err error) bool {
	var handshakeErr *wss.HandshakeError
	return !errors.As(err, &handshakeErr) || handshakeErr.Code == "duplicate_peer"
}

func sleepBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}
