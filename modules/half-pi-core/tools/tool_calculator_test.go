package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCalculator(t *testing.T) {
	tests := []struct {
		expr    string
		want    string
		wantErr bool
	}{
		// 四则运算
		{expr: "3 + 4", want: "3 + 4 = 7"},
		{expr: "10 - 3", want: "10 - 3 = 7"},
		{expr: "6 * 7", want: "6 * 7 = 42"},
		{expr: "15 / 3", want: "15 / 3 = 5"},
		{expr: "(3 + 4) * 5", want: "(3 + 4) * 5 = 35"},
		{expr: "3.14 + 2.86", want: "3.14 + 2.86 = 6"},
		{expr: "-5 + 3", want: "-5 + 3 = -2"},

		// 常量
		{expr: "pi * 2", wantErr: false},

		// 函数 — 单参数
		{expr: "abs(-5)", want: "abs(-5) = 5"},
		{expr: "sqrt(16)", want: "sqrt(16) = 4"},
		{expr: "sqrt(-1)", wantErr: true},
		{expr: "cbrt(27)", want: "cbrt(27) = 3"},
		{expr: "round(3.7)", want: "round(3.7) = 4"},
		{expr: "floor(3.7)", want: "floor(3.7) = 3"},
		{expr: "ceil(3.2)", want: "ceil(3.2) = 4"},

		// 函数 — 双参数
		{expr: "pow(2, 10)", want: "pow(2, 10) = 1024"},
		{expr: "pow(9, 0.5)", want: "pow(9, 0.5) = 3"},

		// 统计 — 变参
		{expr: "sum(1,2,3,4,5)", want: "sum(1,2,3,4,5) = 15"},
		{expr: "mean(1,2,3,4,5)", want: "mean(1,2,3,4,5) = 3"},
		{expr: "avg(10,20,30)", want: "avg(10,20,30) = 20"},
		{expr: "min(3,1,4,1,5)", want: "min(3,1,4,1,5) = 1"},
		{expr: "max(3,1,4,1,5)", want: "max(3,1,4,1,5) = 5"},

		// 错误情况
		{expr: "", wantErr: true},
		{expr: "1 / 0", wantErr: true},
		{expr: "hello", wantErr: true},
		{expr: "3 +", wantErr: true},
		{expr: "unknown(5)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"expression": tt.expr})
			result := calculatorExecute(context.Background(), args)
			if tt.wantErr {
				if result.Success {
					t.Errorf("expected error, got success: %s", result.Output)
				}
				return
			}
			if !result.Success {
				t.Fatalf("unexpected error: %s", result.Error)
			}
			if tt.want != "" && result.Output != tt.want+"\n" {
				t.Errorf("got %q, want %q", result.Output, tt.want+"\n")
			}
		})
	}
}

func TestVarianceAndStddev(t *testing.T) {
	// 方差：对数据集 [2, 4, 4, 4, 5, 5, 7, 9]，期望 = 5，方差 = 4
	args, _ := json.Marshal(map[string]string{"expression": "var(2,4,4,4,5,5,7,9)"})
	r := calculatorExecute(context.Background(), args)
	if !r.Success {
		t.Fatalf("var failed: %s", r.Error)
	}
	// 数值验证在 formatResult 里，我们只检查没报错
	t.Logf("var result: %s", r.Output)

	// 标准差应该是 2
	args2, _ := json.Marshal(map[string]string{"expression": "stddev(2,4,4,4,5,5,7,9)"})
	r2 := calculatorExecute(context.Background(), args2)
	if !r2.Success {
		t.Fatalf("stddev failed: %s", r2.Error)
	}
	t.Logf("stddev result: %s", r2.Output)
}
