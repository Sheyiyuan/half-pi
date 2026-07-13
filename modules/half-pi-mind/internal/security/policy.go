// Package security 实现风险模式和黑白名单检查。
package security

import "strings"

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

// rule 是一条黑白名单规则。
type rule struct {
	pattern string   // 匹配模式
	desc    string   // 规则说明，用于日志和提示
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
			{pattern: "chmod -R 000 /", desc: "锁定系统"},
			{pattern: "wget -O - | sh", desc: "远程执行未验证脚本"},
			{pattern: "curl | sh", desc: "远程执行未验证脚本"},
			{pattern: "mv /* /dev/null", desc: "移动系统文件到空设备"},
			{pattern: "shutdown", desc: "关机"},
			{pattern: "reboot", desc: "重启"},
			{pattern: "init 0", desc: "关机"},
			{pattern: "poweroff", desc: "关机"},
		},
	}
}

// Decision 是安全检查的结果。
type Decision int

const (
	Allow    Decision = iota // 允许执行
	Deny                     // 拒绝执行（命中黑名单）
	NeedApproval             // 灰名单，需要用户确认
)

// Check 对命令进行安全检查，返回决策结果和原因。
func (p *Policy) Check(cmd string) (Decision, string) {
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))

	// 黑名单：直接拒绝
	for _, r := range p.blacklist {
		if strings.Contains(cmdLower, r.pattern) {
			return Deny, r.desc
		}
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
		// Trust 模式 AI 自行判断，全放行但记录
		return Allow, ""

	default: // ModeNormal
		// 普通模式包含写入、删除、网络等敏感操作需审批
		sensitive := []rule{
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
		for _, r := range sensitive {
			if strings.Contains(cmdLower, r.pattern) {
				return NeedApproval, r.desc
			}
		}
		return Allow, ""
	}
}

// ── 全局默认策略 ──

var defaultPolicy = New()

// SetPolicy 替换全局默认策略。
func SetPolicy(p *Policy) {
	defaultPolicy = p
}

// Check 使用全局默认策略检查命令。
func Check(cmd string) (Decision, string) {
	return defaultPolicy.Check(cmd)
}
