// /skill 命令：查看当前会话组可见技能、加载告警，并重新扫描技能目录。
package repl

import (
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
)

func (r *Repl) handleSkill(arg string) {
	switch arg {
	case "", "list":
		r.handleSkillList()
	case "reload":
		r.handleSkillReload()
	case "warnings":
		r.handleSkillWarnings()
	default:
		r.emit(events.LevelWarn, events.TypeSystem, fmt.Sprintf("unknown skill command: %s (list/reload/warnings)", arg))
	}
}

func (r *Repl) handleSkillList() {
	skills := r.core.Skills()
	if skills == nil {
		r.emit(events.LevelWarn, events.TypeSystem, "skill system is not initialized")
		return
	}
	summaries := skills.SummariesForGroup(r.groupID)
	if len(summaries) == 0 {
		fmt.Println("no skills visible in this group")
		return
	}
	for _, summary := range summaries {
		marker := " "
		if summary.Always {
			marker = "*"
		}
		line := fmt.Sprintf(" %s %-20s %s", marker, summary.Name, summary.Description)
		if len(summary.Tags) > 0 {
			line += fmt.Sprintf(" [%s]", strings.Join(summary.Tags, ", "))
		}
		fmt.Println(line)
	}
	fmt.Printf("%d skills (* = always active)\n", len(summaries))
}

func (r *Repl) handleSkillReload() {
	skills := r.core.Skills()
	if skills == nil {
		r.emit(events.LevelWarn, events.TypeSystem, "skill system is not initialized")
		return
	}
	before := len(skills.SummariesForGroup(r.groupID))
	if err := skills.Reload(); err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("reload skills: %v", err))
		return
	}
	after := skills.SummariesForGroup(r.groupID)
	// Store 由所有 Actor 共享，reload 对全部会话立即生效；
	// revision/digest 变化会让进行中的模型请求在 admission 阶段 fail closed。
	r.emit(events.LevelInfo, events.TypeSystem,
		fmt.Sprintf("skills reloaded: %d visible in this group (was %d)", len(after), before))
	r.printSkillWarnings()
}

func (r *Repl) handleSkillWarnings() {
	skills := r.core.Skills()
	if skills == nil {
		r.emit(events.LevelWarn, events.TypeSystem, "skill system is not initialized")
		return
	}
	if len(skills.Warnings()) == 0 {
		fmt.Println("no skill load warnings")
		return
	}
	r.printSkillWarnings()
}

func (r *Repl) printSkillWarnings() {
	for _, warning := range r.core.Skills().Warnings() {
		r.emit(events.LevelWarn, events.TypeSystem, "skill: "+warning)
	}
}
