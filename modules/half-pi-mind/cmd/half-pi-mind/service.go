package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
)

// runService 以后台服务模式运行 Mind Hub，并写入 PID 文件。
func runService(env *setup.Env, bus *events.EventBus, serverErrors <-chan error) error {
	pidFile := filepath.Join(env.HomeDir, "mind.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	defer os.Remove(pidFile)

	bus.PublishSync(events.New("", "mind", events.LevelInfo, events.TypeSystem, "Mind 服务已启动"))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-ctx.Done():
	case err, ok := <-serverErrors:
		if !ok {
			return fmt.Errorf("Hub server stopped unexpectedly")
		}
		return err
	}

	bus.PublishSync(events.New("", "mind", events.LevelInfo, events.TypeSystem, "Mind 正在关闭..."))
	return nil
}
