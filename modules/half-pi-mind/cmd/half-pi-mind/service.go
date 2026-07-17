package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
)

// runService 以后台服务模式运行 Mind Hub，并写入 PID 文件。
func runService(env *setup.Env, bus *events.EventBus) {
	pidFile := filepath.Join(env.HomeDir, "mind.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write pid file: %v\n", err)
	}
	defer os.Remove(pidFile)

	bus.PublishSync(events.New("", "mind", events.LevelInfo, events.TypeSystem, "Mind 服务已启动"))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	<-ctx.Done()

	bus.PublishSync(events.New("", "mind", events.LevelInfo, events.TypeSystem, "Mind 正在关闭..."))
}
