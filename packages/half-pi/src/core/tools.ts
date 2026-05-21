/**
 * Tool name definitions and metadata for half-pi.
 *
 * Tool implementations are in tool-impls.ts.
 */

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

// ─── Tool snippets (for system prompt display) ───

export const TOOL_SNIPPETS: Record<string, string> = {
	read: "Read file contents with line numbers and pagination",
	bash: "Execute shell commands on the local machine. Use for builds, git, npm, testing.",
	edit: "Targeted find-and-replace edits in files. Prefer this over write for small changes.",
	write: "Write full content to a file. Use for creating new files or complete rewrites.",
	grep: "Search file contents with regex. Faster than bash grep/rg (uses ripgrep).",
	find: "Find files by glob pattern. Faster than bash find (uses fd).",
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
