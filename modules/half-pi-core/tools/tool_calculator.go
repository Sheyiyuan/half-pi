// Package tools 提供 calculator 工具：计算数学表达式的值。
// 支持四则运算、函数调用和基本统计，零外部依赖（纯 Go 标准库）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name: "calculator",
		Description: "计算数学表达式的值。支持四则运算（+ - * /）、括号、常量和函数调用。" +
			"函数：abs pow sqrt cbrt log ln log10 sin cos tan round floor ceil，" +
			"统计：mean avg var variance stddev min max sum。例：" +
			"\"(3.14 + 2.86) * 5\"、\"pow(2, 10)\"、\"mean(1,2,3,4,5)\"",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "expression", Type: "string", Description: "要计算的表达式，如 \"pow(2, 10)\" 或 \"mean(1,2,3,4,5)\""},
			},
			Required: []string{"expression"},
		},
		Execute: calculatorExecute,
	})
}

func calculatorExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var p struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("解析参数失败: %v", err)}
	}
	expr := strings.TrimSpace(p.Expression)
	if expr == "" {
		return &executor.ToolResult{Error: "表达式不能为空"}
	}

	result, err := eval(expr)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("计算错误: %v", err)}
	}

	output := formatResult(expr, result)
	return &executor.ToolResult{Success: true, Output: output + "\n"}
}

func formatResult(expr string, v float64) string {
	if v == math.Trunc(v) && !math.IsInf(v, 0) && math.Abs(v) < 1<<53 {
		return fmt.Sprintf("%s = %d", expr, int64(v))
	}
	return fmt.Sprintf("%s = %v", expr, v)
}

// eval 用 Go 标准库表达式解析器解析并计算。
func eval(expr string) (float64, error) {
	// var 是 Go 关键字，替换为别名 variance 再解析
	expr = strings.ReplaceAll(expr, "var(", "variance(")
	astExpr, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("无法解析表达式: %w", err)
	}
	return evalAST(astExpr)
}

func evalAST(node ast.Node) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		return strconv.ParseFloat(n.Value, 64)
	case *ast.ParenExpr:
		return evalAST(n.X)
	case *ast.UnaryExpr:
		return evalUnary(n)
	case *ast.BinaryExpr:
		return evalBinary(n)
	case *ast.Ident:
		switch strings.ToLower(n.Name) {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		default:
			return 0, fmt.Errorf("未知符号: %s", n.Name)
		}
	case *ast.CallExpr:
		return evalCall(n)
	default:
		return 0, fmt.Errorf("不支持的表达式: %T", node)
	}
}

func evalUnary(unary *ast.UnaryExpr) (float64, error) {
	x, err := evalAST(unary.X)
	if err != nil {
		return 0, err
		// TODO(社老师): 这里也可以直接透传负号
	}
	switch unary.Op {
	case token.SUB:
		return -x, nil
	case token.ADD:
		return x, nil
	default:
		return 0, fmt.Errorf("不支持的一元运算符: %v", unary.Op)
	}
}

func evalBinary(bin *ast.BinaryExpr) (float64, error) {
	x, err := evalAST(bin.X)
	if err != nil {
		return 0, err
	}
	y, err := evalAST(bin.Y)
	if err != nil {
		return 0, err
	}
	switch bin.Op {
	case token.ADD:
		return x + y, nil
	case token.SUB:
		return x - y, nil
	case token.MUL:
		return x * y, nil
	case token.QUO:
		if y == 0 {
			return 0, fmt.Errorf("除数不能为 0")
		}
		return x / y, nil
	default:
		return 0, fmt.Errorf("不支持的运算符: %v", bin.Op)
	}
}

func evalCall(call *ast.CallExpr) (float64, error) {
	// 只支持简单函数名 Ident，不支持 math.Pow 这种 SelectorExpr
	fn, ok := call.Fun.(*ast.Ident)
	if !ok {
		return 0, fmt.Errorf("不支持的函数调用语法")
	}

	// 计算所有参数
	args := make([]float64, len(call.Args))
	for i, arg := range call.Args {
		v, err := evalAST(arg)
		if err != nil {
			return 0, fmt.Errorf("参数 %d: %w", i+1, err)
		}
		args[i] = v
	}

	switch strings.ToLower(fn.Name) {
	// 单参数函数
	case "abs":
		return check1("abs", args, func(v float64) float64 { return math.Abs(v) })
	case "sqrt":
		return check1("sqrt", args, func(v float64) float64 {
			if v < 0 {
				return math.NaN()
			}
			return math.Sqrt(v)
		})
	case "cbrt":
		return check1("cbrt", args, math.Cbrt)
	case "log", "ln":
		return check1(fn.Name, args, math.Log)
	case "log10":
		return check1("log10", args, math.Log10)
	case "sin":
		return check1("sin", args, math.Sin)
	case "cos":
		return check1("cos", args, math.Cos)
	case "tan":
		return check1("tan", args, math.Tan)
	case "round":
		return check1("round", args, math.Round)
	case "floor":
		return check1("floor", args, math.Floor)
	case "ceil":
		return check1("ceil", args, math.Ceil)

	// 双参数函数
	case "pow":
		if len(args) < 2 {
			return 0, fmt.Errorf("pow 需要 2 个参数，收到 %d", len(args))
		}
		return math.Pow(args[0], args[1]), nil

	// 变参 / 统计
	case "sum":
		return sum(args), nil
	case "mean", "avg":
		if len(args) < 1 {
			return 0, fmt.Errorf("%s 需要至少 1 个参数", fn.Name)
		}
		return sum(args) / float64(len(args)), nil
	case "var", "variance":
		if len(args) < 2 {
			return 0, fmt.Errorf("%s 需要至少 2 个参数", fn.Name)
		}
		return variance(args), nil
	case "stddev":
		if len(args) < 2 {
			return 0, fmt.Errorf("stddev 需要至少 2 个参数")
		}
		return math.Sqrt(variance(args)), nil
	case "min":
		if len(args) < 1 {
			return 0, fmt.Errorf("min 需要至少 1 个参数")
		}
		return minOf(args), nil
	case "max":
		if len(args) < 1 {
			return 0, fmt.Errorf("max 需要至少 1 个参数")
		}
		return maxOf(args), nil

	default:
		return 0, fmt.Errorf("未知函数: %s", fn.Name)
	}
}

// check1 检查正好 1 个参数后执行一元函数 f。
func check1(name string, args []float64, f func(float64) float64) (float64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s 需要 1 个参数，收到 %d", name, len(args))
	}
	r := f(args[0])
	if math.IsNaN(r) {
		return 0, fmt.Errorf("%s(%v) 结果无定义", name, args[0])
	}
	return r, nil
}

// --- 统计辅助函数 ---

func sum(vs []float64) float64 {
	s := 0.0
	for _, v := range vs {
		s += v
	}
	return s
}

func variance(vs []float64) float64 {
	m := sum(vs) / float64(len(vs))
	ss := 0.0
	for _, v := range vs {
		d := v - m
		ss += d * d
	}
	return ss / float64(len(vs))
}

func minOf(vs []float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxOf(vs []float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
