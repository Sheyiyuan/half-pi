/**
 * half-pi public API.
 */

export { AgentSession } from "./core/agent-session.ts";
export type { AgentSessionOptions } from "./core/agent-session.ts";

export { buildSystemPrompt } from "./core/system-prompt.ts";
export type { BuildSystemPromptOptions } from "./core/system-prompt.ts";

export { loadSoul } from "./core/soul-loader.ts";
export type { Soul } from "./core/soul-loader.ts";

export { loadAllSkills, createSkill, deleteSkill, formatSkillsForPrompt } from "./core/skills.ts";
export type { Skill } from "./core/skills.ts";

export { createHalfPiTools } from "./core/tool-impls.ts";

export {
	TOOL_NAMES,
	TOOL_SNIPPETS,
	DEFAULT_TOOLS,
} from "./core/tools.ts";
export type { ToolName } from "./core/tools.ts";

export { getHalfPiDir, getSoulPath, getSkillsDir, getMemoryDir, getSessionsDir } from "./config.ts";
