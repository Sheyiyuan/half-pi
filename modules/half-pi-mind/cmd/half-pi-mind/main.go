package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

// REPLApprover 实现 agentcore.Approver，在终端交互确认。
type REPLApprover struct {
	scanner *bufio.Scanner
}

func (a *REPLApprover) Confirm(toolName, reason string) agentcore.ConfirmResult {
	fmt.Fprintf(os.Stderr, "\n⚠️  需要确认 [%s] %s\n", toolName, reason)
	fmt.Fprint(os.Stderr, "  [y] 允许一次  [n] 拒绝一次  [Y] 始终允许  [N] 始终拒绝: ")

	if !a.scanner.Scan() {
		return agentcore.ConfirmDeny
	}
	switch strings.TrimSpace(a.scanner.Text()) {
	case "y":
		return agentcore.ConfirmAllow
	case "Y":
		return agentcore.ConfirmAllowAlways
	case "N":
		return agentcore.ConfirmDenyAlways
	default:
		return agentcore.ConfirmDeny
	}
}

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "请设置 DEEPSEEK_API_KEY 环境变量")
		os.Exit(1)
	}

	provider := llm.NewOpenAI(
		"https://api.deepseek.com/v1",
		apiKey,
		"deepseek-chat",
	)

	exec := local.New()

	bus := events.NewEventBus()
	bus.Subscribe(events.NewConsoleWriter())

	core, err := agentcore.New(provider, exec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}
	core.Bus = bus

	scanner := bufio.NewScanner(os.Stdin)
	core.SetApprover(&REPLApprover{scanner: scanner})
	defer bus.Close()

	fmt.Println("half-pi mind ready")
	fmt.Println("输入 /mode <normal|trust|yolo> 切换模式")
	fmt.Println("输入 /debug 切换调试模式，输入 exit 退出")
	fmt.Println()

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("bye")
			break
		}
		if input == "/debug" {
			core.Debug = !core.Debug
			bus.PublishSync(events.New("", "repl", events.LevelInfo, events.TypeSystem,
				fmt.Sprintf("调试模式: %v", core.Debug)))
			continue
		}
		if input == "/mode" {
			bus.PublishSync(events.New("", "repl", events.LevelInfo, events.TypeSystem,
				fmt.Sprintf("当前模式: %s", core.Mode)))
			continue
		}
		if strings.HasPrefix(input, "/mode ") {
			mode := strings.TrimSpace(strings.TrimPrefix(input, "/mode "))
			switch mode {
			case "strict", "normal", "trust", "yolo":
				core.SetMode(mode)
				bus.PublishSync(events.New("", "repl", events.LevelInfo, events.TypeModeChange,
					fmt.Sprintf("安全模式已切换为: %s", mode)))
			default:
				bus.PublishSync(events.New("", "repl", events.LevelWarn, events.TypeSystem,
					fmt.Sprintf("未知模式: %s（支持: strict, normal, trust, yolo）", mode)))
			}
			continue
		}

		response, err := core.Chat(context.Background(), input)
		if err != nil {
			bus.PublishSync(events.New("", "repl", events.LevelError, events.TypeSystem,
				fmt.Sprintf("错误: %v", err)))
			continue
		}
		fmt.Println(response)
		fmt.Println()
	}
}
