// Package security 实现风险模式和黑白名单检查。
package security

import (
	"strings"
	"sync"
)

// Mode 表示当前的风险模式。
type Mode int

const (
	ModeStrict Mode = iota // 只允许白名单命令
	ModeNormal             // 灰名单命令需要审批（默认）
	ModeTrust              // AI 自行判断风险
	ModeYOLO               // 无条件执行（物理黑名单除外）
)

// Policy 持有安全规则。
type Policy struct {
	Mode      Mode
	blacklist []rule
	whitelist []rule
}

// RiskClass 是与安全模式无关的确定性风险分类。
type RiskClass string

const (
	RiskSafe      RiskClass = "safe"
	RiskSensitive RiskClass = "sensitive"
	RiskForbidden RiskClass = "forbidden"
)

// rule 是一条黑白名单规则。
type rule struct {
	pattern string // 匹配模式
	desc    string // 规则说明，用于日志和提示
}

// New 创建一个默认策略（Normal 模式）。
func New() *Policy {
	return &Policy{
		Mode: ModeNormal,
		blacklist: []rule{
			{pattern: "rm -rf /", desc: "删除根目录"},
			{pattern: "rm -rf /*", desc: "删除根目录"},
			{pattern: "mkfs", desc: "格式化磁盘"},
			{pattern: "dd if=/dev/zero of=/dev/sd", desc: "覆写磁盘"},
			{pattern: ":(){ :|:& };:", desc: "fork 炸弹"},
			{pattern: "> /dev/sd", desc: "覆写磁盘"},
			{pattern: "chmod -r 000 /", desc: "锁定系统"},
			{pattern: "wget -o - | sh", desc: "远程执行未验证脚本"},
			{pattern: "curl | sh", desc: "远程执行未验证脚本"},
			{pattern: "mv /* /dev/null", desc: "移动系统文件到空设备"},
			{pattern: "shutdown", desc: "关机"},
			{pattern: "reboot", desc: "重启"},
			{pattern: "init 0", desc: "关机"},
			{pattern: "poweroff", desc: "关机"},
		},
	}
}

// Clone 返回可独立修改 mode 的策略副本。
func (p *Policy) Clone() *Policy {
	if p == nil {
		return New()
	}
	clone := *p
	clone.blacklist = append([]rule(nil), p.blacklist...)
	clone.whitelist = append([]rule(nil), p.whitelist...)
	return &clone
}

// WithMode 返回使用指定模式的策略副本。
func (p *Policy) WithMode(mode Mode) *Policy {
	clone := p.Clone()
	clone.Mode = mode
	return clone
}

// Classify 仅做确定性风险分类，不执行 mode routing。
func (p *Policy) Classify(cmd string) (RiskClass, string) {
	if p == nil {
		p = New()
	}
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))
	for _, r := range p.blacklist {
		if strings.Contains(cmdLower, r.pattern) {
			return RiskForbidden, r.desc
		}
	}
	for _, r := range sensitiveRules {
		if strings.Contains(cmdLower, r.pattern) {
			return RiskSensitive, r.desc
		}
	}
	return RiskSafe, ""
}

// Decision 是安全检查的结果。
type Decision int

const (
	Allow        Decision = iota // 允许执行
	Deny                         // 拒绝执行（命中黑名单）
	NeedApproval                 // 灰名单，需要用户确认
)

// Check 对命令进行安全检查，返回决策结果和原因。
func (p *Policy) Check(cmd string) (Decision, string) {
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))

	risk, riskReason := p.Classify(cmdLower)
	if risk == RiskForbidden {
		return Deny, riskReason
	}

	switch p.Mode {
	case ModeStrict:
		// 严格模式下只允许白名单
		for _, r := range p.whitelist {
			if strings.Contains(cmdLower, r.pattern) {
				return Allow, ""
			}
		}
		return NeedApproval, "严格模式，不在白名单中"

	case ModeYOLO:
		return Allow, ""

	case ModeTrust:
		// Reviewer routing 由 Mind Authorizer 处理；Policy 只保留硬拒绝。
		return Allow, ""

	default: // ModeNormal
		if risk == RiskSensitive {
			return NeedApproval, riskReason
		}
		return Allow, ""
	}
}

var sensitiveRules = []rule{
	{pattern: "rm ", desc: "删除操作"},
	{pattern: "mv ", desc: "移动/重命名"},
	{pattern: "> ", desc: "输出重定向到文件"},
	{pattern: ">>", desc: "追加到文件"},
	{pattern: "dd ", desc: "磁盘操作"},
	{pattern: "sudo ", desc: "提权操作"},
	{pattern: "apt ", desc: "包管理操作"},
	{pattern: "apt-get", desc: "包管理操作"},
	{pattern: "pip ", desc: "安装依赖"},
	{pattern: "npm ", desc: "安装依赖"},
	{pattern: "chmod", desc: "修改权限"},
	{pattern: "chown", desc: "修改所有者"},
	{pattern: "kill ", desc: "终止进程"},
	{pattern: "systemctl", desc: "系统服务管理"},
	{pattern: "docker ", desc: "容器操作"},
}

// ── 全局默认策略 ──

var (
	defaultMu     sync.RWMutex
	defaultPolicy = New()
)

// SetPolicy 替换全局默认策略。
func SetPolicy(p *Policy) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if p == nil {
		p = New()
	}
	defaultPolicy = p
}

// SetMode 更新全局默认策略的安全模式。
func SetMode(mode Mode) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultPolicy.Mode = mode
}

// Check 使用全局默认策略检查命令。
func Check(cmd string) (Decision, string) {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultPolicy.Check(cmd)
}
