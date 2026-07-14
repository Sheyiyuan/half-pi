// Half-Pi Hand — 远程执行节点。
// 通过 WebSocket 连接 Mind，接收 RPC 工具执行请求并返回结果。
//
// 配置优先级（高到低）：CLI flag → 环境变量 → 配置文件
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/hand"
)

func main() {
	cfgPath := flag.String("config", "", "配置文件路径（默认 ~/.half-pi/hand/config.toml）")
	server := flag.String("server", "", "Mind WebSocket 地址")
	token := flag.String("token", "", "认证令牌")
	id := flag.String("id", "", "Hand 唯一标识")
	flag.Parse()

	// 加载配置文件
	path := *cfgPath
	if path == "" {
		path = config.DefaultPath()
		// 首次运行写入默认配置
		if err := config.WriteDefault(path); err != nil {
			fmt.Fprintf(os.Stderr, "create config: %v\n", err)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置文件加载失败: %v\n", err)
		os.Exit(1)
	}

	// CLI flag 覆盖
	if *server != "" {
		cfg.Server.URL = *server
	}
	if *token != "" {
		cfg.Server.Token = *token
	}
	if *id != "" {
		cfg.Hand.ID = *id
	}

	// 默认值
	if cfg.Hand.ID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "hand"
		}
		cfg.Hand.ID = hostname
	}
	if cfg.Server.URL == "" {
		cfg.Server.URL = "ws://localhost:8080/ws"
	}

	conn, err := wss.NewClient(cfg.Server.URL).ConnectAndRegister(cfg.Hand.ID, "hand", cfg.Server.Token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接失败: %v\n", err)
		os.Exit(1)
	}
	defer conn.Conn.Close()

	fmt.Fprintf(os.Stderr, "Hand %s 已连接到 %s\n", cfg.Hand.ID, cfg.Server.URL)

	h := hand.New(conn)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := h.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Hand 退出: %v\n", err)
		os.Exit(1)
	}
}
