/**
 * half-pi public API.
 */

export { AgentSession } from "./core/agent-session.js";
export type { AgentSessionOptions } from "./core/agent-session.js";

export { buildSystemPrompt } from "./core/system-prompt.js";
export type { BuildSystemPromptOptions } from "./core/system-prompt.js";

export { loadSoul } from "./core/soul-loader.js";
export type { Soul } from "./core/soul-loader.js";

export { loadAllSkills, createSkill, deleteSkill, formatSkillsForPrompt } from "./core/skills.js";
export type { Skill } from "./core/skills.js";

export { createHalfPiTools } from "./core/tool-impls.js";

export {
	TOOL_NAMES,
	TOOL_SNIPPETS,
	DEFAULT_TOOLS,
} from "./core/tools.js";
export type { ToolName } from "./core/tools.js";

export { getHalfPiDir, getSoulPath, getSkillsDir, getMemoryDir, getSessionsDir } from "./config.js";
