/**
 * Tool registration for half-pi.
 *
 * Registers the core coding tools (read, bash, edit, write, grep, find, ls)
 * plus half-pi specific tools (skill management).
 *
 * Tool implementations are based on pi's coding-agent tools.
 */

import type { AgentTool } from "@earendil-works/pi-agent-core";
import { Type } from "@earendil-works/pi-ai";

// ─── Tool name type ───
export const TOOL_NAMES = [
	"read",
	"bash",
	"edit",
	"write",
	"grep",
	"find",
	"ls",
	"skill_create",
	"skill_list",
	"skill_delete",
	"soul_view",
] as const;
export type ToolName = (typeof TOOL_NAMES)[number];

// ─── Tool schemas (TypeBox) ───

export const ReadToolInput = Type.Object({
	path: Type.String({ description: "Path to the file to read" }),
	offset: Type.Optional(Type.Number({ description: "Line number to start reading from (1-indexed)" })),
	limit: Type.Optional(Type.Number({ description: "Maximum number of lines to read" })),
});

export const BashToolInput = Type.Object({
	command: Type.String({ description: "The shell command to execute" }),
	timeout: Type.Optional(Type.Number({ description: "Max seconds to wait" })),
	workdir: Type.Optional(Type.String({ description: "Working directory" })),
});

export const EditToolInput = Type.Object({
	path: Type.String({ description: "File path to edit" }),
	old_string: Type.String({ description: "Text to find" }),
	new_string: Type.String({ description: "Replacement text" }),
});

export const WriteToolInput = Type.Object({
	path: Type.String({ description: "Path to the file to write" }),
	content: Type.String({ description: "Complete content to write" }),
});

export const GrepToolInput = Type.Object({
	pattern: Type.String({ description: "Regex pattern to search for" }),
	path: Type.Optional(Type.String({ description: "Directory or file to search in" })),
	file_glob: Type.Optional(Type.String({ description: "Filter files by pattern" })),
});

export const FindToolInput = Type.Object({
	pattern: Type.String({ description: "Glob pattern for file name matching" }),
	path: Type.Optional(Type.String({ description: "Directory to search in" })),
});

export const LsToolInput = Type.Object({
	path: Type.Optional(Type.String({ description: "Directory to list" })),
});

export const SkillCreateInput = Type.Object({
	name: Type.String({ description: "Skill name (lowercase, hyphens, max 64 chars)" }),
	description: Type.String({ description: "What this skill does" }),
	content: Type.String({ description: "Full markdown content (steps, code, pitfalls)" }),
	triggers: Type.Optional(Type.Array(Type.String(), { description: "Trigger conditions" })),
});

export const SkillDeleteInput = Type.Object({
	name: Type.String({ description: "Name of the skill to delete" }),
});

export const SoulViewInput = Type.Object({});

// ─── Tool snippets (for system prompt) ───

export const TOOL_SNIPPETS: Record<string, string> = {
	read: "Read file contents with line numbers and pagination",
	bash: "Execute shell commands on the local machine. Use for builds, git, npm, testing.",
	edit: "Targeted find-and-replace edits in files. Prefer this over write for small changes.",
	write: "Write full content to a file. Use for creating new files or complete rewrites.",
	grep: "Search file contents with regex. Faster than bash grep/rg.",
	find: "Find files by glob pattern. Faster than bash find.",
	ls: "List directory contents. Faster than bash ls.",
	skill_create: "Create a new skill — a reusable workflow document that persists across sessions",
	skill_list: "List all installed skills",
	skill_delete: "Delete a skill by name",
	soul_view: "View the current SOUL.md identity file",
};

export const DEFAULT_TOOLS: ToolName[] = [
	"read",
	"bash",
	"edit",
	"write",
	"grep",
	"find",
	"ls",
	"skill_create",
	"skill_list",
	"skill_delete",
	"soul_view",
];
