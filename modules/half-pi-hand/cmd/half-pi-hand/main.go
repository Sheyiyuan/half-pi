// Half-Pi Hand — 远程执行节点。
// 通过 WebSocket 连接 Mind，接收 RPC 工具执行请求并返回结果。
//
// 配置优先级（高到低）：CLI flag → 环境变量 → 配置文件
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/hand"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/taskmanager"
)

func main() {
	cfgPath := flag.String("config", "", "配置文件路径（默认 ~/.half-pi/hand/config.toml）")
	server := flag.String("server", "", "Mind WebSocket 地址")
	token := flag.String("token", "", "认证令牌")
	applicationKey := flag.String("application-key", "", "应用密钥")
	id := flag.String("id", "", "Hand 唯一标识")
	flag.Parse()

	path := *cfgPath
	if path == "" {
		path = config.DefaultPath()
		if err := config.WriteDefault(path); err != nil {
			fmt.Fprintf(os.Stderr, "create config: %v\n", err)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置文件加载失败: %v\n", err)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "Hand 凭据无效: %v\n", err)
		os.Exit(1)
	}

	if cfg.Hand.WorkDir != "" {
		if err := os.Chdir(cfg.Hand.WorkDir); err != nil {
			fmt.Fprintf(os.Stderr, "切换工作目录失败: %v\n", err)
			os.Exit(1)
		}
	}

	info := hand.CollectHandInfo()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	manager, err := taskmanager.New(taskmanager.Config{
		Dir: cfg.Hand.Tasks.Dir, MaxRunning: cfg.Hand.Tasks.MaxRunning,
		MaxRuntime: cfg.Hand.Tasks.MaxRuntimeDuration(), MaxLogBytes: cfg.Hand.Tasks.MaxLogBytes,
		Retention: cfg.Hand.Tasks.RetentionDuration(), MaxRetained: cfg.Hand.Tasks.MaxRetained,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "后台任务管理器启动失败: %v\n", err)
		os.Exit(1)
	}
	defer manager.Close()

	backoff := 1 * time.Second
	maxBackoff := time.Duration(cfg.Hand.Retry.MaxBackoff) * time.Second

	for {
		conn, err := wss.NewClient(cfg.Server.URL).ConnectAndRegister(wss.Credentials{
			Label: cfg.Hand.ID, Type: "hand", Token: cfg.Server.Token,
			ApplicationKey: cfg.Server.ApplicationKey, Info: info,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !shouldRetry(err) {
				fmt.Fprintf(os.Stderr, "永久握手失败: %v\n", err)
				return
			}
			fmt.Fprintf(os.Stderr, "连接失败: %v，%v 后重试\n", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = sleepBackoff(backoff, maxBackoff)
			continue
		}
		fmt.Fprintf(os.Stderr, "Hand %s 已连接到 %s\n", cfg.Hand.ID, cfg.Server.URL)
		backoff = 1 * time.Second

		h := hand.NewWithTaskManager(conn, cfg, manager)
		err = h.Serve(ctx)
		conn.Conn.Close()
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Hand 退出: %v，%v 后重连\n", err, backoff)
		} else {
			fmt.Fprintf(os.Stderr, "Hand 连接已断开，%v 后重连\n", backoff)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = sleepBackoff(backoff, maxBackoff)
	}
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
