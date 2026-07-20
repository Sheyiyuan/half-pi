// Half-Pi Face 提供人类和 Agent 共用的远程交互入口。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/mattn/go-isatty"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/headless"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/tui"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Face exited: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, input io.Reader, output, logs io.Writer) error {
	flags := flag.NewFlagSet("half-pi-face", flag.ContinueOnError)
	flags.SetOutput(logs)
	configPath := flags.String("config", "", "Face config path")
	server := flags.String("server", "", "Mind WebSocket URL")
	token := flags.String("token", "", "Face token")
	applicationKey := flags.String("application-key", "", "Face application key")
	id := flags.String("id", "", "Face credential label")
	mode := flags.String("mode", "", "Face mode")
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(output, "half-pi-face version dev")
		return err
	}

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
		if err := config.WriteDefault(path); err != nil {
			return fmt.Errorf("create default config: %w", err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
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
		cfg.Face.ID = *id
	}
	if *mode != "" {
		cfg.Face.Mode = *mode
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	dialer := client.NewDialer(cfg)
	switch cfg.Face.Mode {
	case config.ModeHeadless:
		conn, err := dialer.Connect(ctx)
		if err != nil {
			return err
		}
		return headless.Run(ctx, conn, input, output)
	case config.ModeTUI:
		inputFile, inputOK := input.(*os.File)
		outputFile, outputOK := output.(*os.File)
		if !inputOK || !outputOK || !isTerminal(inputFile) || !isTerminal(outputFile) {
			return fmt.Errorf("TUI mode requires interactive terminal stdin/stdout; use --mode headless for pipes")
		}
		return tui.Run(ctx, dialer, inputFile, outputFile)
	default:
		return fmt.Errorf("unsupported Face mode %q", cfg.Face.Mode)
	}
}

func isTerminal(file *os.File) bool {
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
