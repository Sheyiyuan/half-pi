/**
 * Tool factory implementations for half-pi.
 *
 * Uses TypeBox schemas and pi's AgentTool interface.
 * Simplified from pi's coding-agent tools — no rendering, no extensions, just core logic.
 */

import { readFileSync, readdirSync, writeFileSync, mkdirSync, existsSync, unlinkSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { sync as spawnSync } from "cross-spawn";
import { Type, type Static } from "@earendil-works/pi-ai";
import type { AgentTool, AgentToolResult } from "@earendil-works/pi-agent-core";
import { createSkill, deleteSkill, loadAllSkills, type Skill } from "./skills.js";
import { loadSoul } from "./soul-loader.js";
import { MemoryStore, type MemoryEntry, type MemoryScope, type MemoryType, type MemoryPriority } from "./memory-store.js";
import type { ToolName } from "./tools.js";

// ─── Helpers ───

function resolveSafe(cwd: string, inputPath: string): string {
	return resolve(cwd, inputPath);
}

function textResult(text: string, details?: Record<string, unknown>): AgentToolResult<Record<string, unknown> | undefined> {
	return {
		content: [{ type: "text", text }],
		details,
	};
}

function okText(text: string): AgentToolResult<undefined> {
	return { content: [{ type: "text", text }], details: undefined };
}

// ─── Read ───

const ReadSchema = Type.Object({
	path: Type.String({ description: "Path to the file to read (relative or absolute)" }),
	offset: Type.Optional(Type.Number({ description: "Line number to start reading from (1-indexed)" })),
	limit: Type.Optional(Type.Number({ description: "Maximum number of lines to read" })),
});

export function createReadTool(cwd: string): AgentTool<typeof ReadSchema, { totalLines: number; path: string }> {
	return {
		name: "read",
		label: "Read",
		description: "Read file contents with line numbers and pagination",
		parameters: ReadSchema,
		execute: async (_id, params) => {
			const filePath = resolveSafe(cwd, params.path);
			const content = readFileSync(filePath, "utf-8");
			const lines = content.split("\n");

			const offset = Math.max(1, params.offset ?? 1);
			const limit = Math.min(params.limit ?? lines.length, lines.length - offset + 1);

			const sliced = lines.slice(offset - 1, offset - 1 + limit);
			const output = sliced.map((line, i) => `${offset + i}|${line}`).join("\n");

			return { content: [{ type: "text", text: output }], details: { totalLines: lines.length, path: filePath } };
		},
	};
}

// ─── Bash ───

const BashSchema = Type.Object({
	command: Type.String({ description: "The shell command to execute" }),
	timeout: Type.Optional(Type.Number({ description: "Max seconds to wait (default: 180)" })),
	workdir: Type.Optional(Type.String({ description: "Working directory for the command" })),
});

export function createBashTool(cwd: string): AgentTool<typeof BashSchema, { exitCode: number | null }> {
	return {
		name: "bash",
		label: "Bash",
		description: "Execute shell commands on the local machine",
		parameters: BashSchema,
		execute: async (_id, params) => {
			const workdir = params.workdir ? resolveSafe(cwd, params.workdir) : cwd;
			const timeoutMs = (params.timeout ?? 180) * 1000;

			try {
				const result = spawnSync(params.command, [], {
					cwd: workdir,
					shell: true,
					timeout: timeoutMs,
					encoding: "utf-8",
					maxBuffer: 10 * 1024 * 1024,
				});

				const output = (result.stdout || "") + (result.stderr ? `\n[stderr]\n${result.stderr}` : "");
				return { content: [{ type: "text", text: output || "(no output)" }], details: { exitCode: result.status } };
			} catch (err) {
				return { content: [{ type: "text", text: `Error: ${String(err)}` }], details: { exitCode: -1 } };
			}
		},
	};
}

// ─── Edit ───

const EditSchema = Type.Object({
	path: Type.String({ description: "File path to edit" }),
	old_string: Type.String({ description: "Text to find in the file" }),
	new_string: Type.String({ description: "Replacement text" }),
});

export function createEditTool(cwd: string): AgentTool<typeof EditSchema, { replaced: number }> {
	return {
		name: "edit",
		label: "Edit",
		description: "Targeted find-and-replace edits in files",
		parameters: EditSchema,
		execute: async (_id, params) => {
			const filePath = resolveSafe(cwd, params.path);
			const content = readFileSync(filePath, "utf-8");

			if (!content.includes(params.old_string)) {
				return { content: [{ type: "text", text: "Error: old_string not found in file" }], details: { replaced: 0 } };
			}

			const count = content.split(params.old_string).length - 1;
			const newContent = content.replace(params.old_string, params.new_string);
			writeFileSync(filePath, newContent, "utf-8");

			return { content: [{ type: "text", text: `Replaced ${count} occurrence(s) in ${filePath}` }], details: { replaced: count } };
		},
	};
}

// ─── Write ───

const WriteSchema = Type.Object({
	path: Type.String({ description: "Path to the file to write" }),
	content: Type.String({ description: "Complete content to write to the file" }),
});

export function createWriteTool(cwd: string): AgentTool<typeof WriteSchema, { bytes: number; path: string }> {
	return {
		name: "write",
		label: "Write",
		description: "Write full content to a file, creating parent directories as needed",
		parameters: WriteSchema,
		execute: async (_id, params) => {
			const filePath = resolveSafe(cwd, params.path);
			mkdirSync(dirname(filePath), { recursive: true });
			writeFileSync(filePath, params.content, "utf-8");

			return { content: [{ type: "text", text: `Wrote ${params.content.length} bytes to ${filePath}` }], details: { bytes: params.content.length, path: filePath } };
		},
	};
}

// ─── Grep ───

const GrepSchema = Type.Object({
	pattern: Type.String({ description: "Regex pattern to search for" }),
	path: Type.Optional(Type.String({ description: "Directory or file to search in (default: cwd)" })),
	file_glob: Type.Optional(Type.String({ description: "Filter files by glob pattern" })),
});

export function createGrepTool(cwd: string): AgentTool<typeof GrepSchema, undefined> {
	return {
		name: "grep",
		label: "Grep",
		description: "Search file contents with regex (uses ripgrep if available)",
		parameters: GrepSchema,
		execute: async (_id, params, _signal, _onUpdate) => {
			const searchPath = params.path ? resolveSafe(cwd, params.path) : cwd;
			const rgArgs = ["--line-number", "--no-heading", "-e", params.pattern, searchPath];
			if (params.file_glob) rgArgs.push("--glob", params.file_glob);

			try {
				const result = spawnSync("rg", rgArgs, { encoding: "utf-8", timeout: 30000, maxBuffer: 5 * 1024 * 1024 });
				const output = result.stdout || "(no matches)";
				return { content: [{ type: "text", text: output.length > 50000 ? output.substring(0, 50000) + "\n...(truncated)" : output }], details: undefined };
			} catch {
				return { content: [{ type: "text", text: "rg not available. Install ripgrep." }], details: undefined };
			}
		},
	};
}

// ─── Find ───

const FindSchema = Type.Object({
	pattern: Type.String({ description: "Glob pattern for file name matching (e.g. '*', '**/*.test')" }),
	path: Type.Optional(Type.String({ description: "Directory to search in (default: cwd)" })),
});

export function createFindTool(cwd: string): AgentTool<typeof FindSchema, undefined> {
	return {
		name: "find",
		label: "Find",
		description: "Find files by glob pattern (uses fd if available)",
		parameters: FindSchema,
		execute: async (_id, params, _signal, _onUpdate) => {
			const searchPath = params.path ? resolveSafe(cwd, params.path) : cwd;
			try {
				const result = spawnSync("fd", ["--type", "f", params.pattern, searchPath], {
					encoding: "utf-8", timeout: 15000, maxBuffer: 5 * 1024 * 1024,
				});
				return { content: [{ type: "text", text: result.stdout || "(no files found)" }], details: undefined };
			} catch {
				return { content: [{ type: "text", text: "fd not available. Install fd-find." }], details: undefined };
			}
		},
	};
}

// ─── Ls ───

const LsSchema = Type.Object({
	path: Type.Optional(Type.String({ description: "Directory to list (default: cwd)" })),
});

export function createLsTool(cwd: string): AgentTool<typeof LsSchema, { count: number }> {
	return {
		name: "ls",
		label: "Ls",
		description: "List directory contents",
		parameters: LsSchema,
		execute: async (_id, params) => {
			const listPath = params.path ? resolveSafe(cwd, params.path) : cwd;
			const entries = readdirSync(listPath, { withFileTypes: true });
			const lines = entries.map((e) => {
				const suffix = e.isDirectory() ? "/" : e.isSymbolicLink() ? "@" : "";
				return `${e.name}${suffix}`;
			});
			return { content: [{ type: "text", text: lines.join("\n") || "(empty directory)" }], details: { count: lines.length } };
		},
	};
}

// ─── Skill Create ───

const SkillCreateSchema = Type.Object({
	name: Type.String({ description: "Skill name (lowercase, hyphens, max 64 chars)" }),
	description: Type.String({ description: "What this skill does" }),
	content: Type.String({ description: "Full markdown content (steps, code, pitfalls)" }),
	triggers: Type.Optional(Type.Array(Type.String(), { description: "Trigger conditions for auto-loading" })),
});

export function createSkillCreateTool(): AgentTool<typeof SkillCreateSchema, { path: string }> {
	return {
		name: "skill_create",
		label: "Create Skill",
		description: "Create a new skill — a reusable workflow document that persists across sessions",
		parameters: SkillCreateSchema,
		execute: async (_id, params) => {
			const filePath = createSkill(params.name, params.description, params.content, params.triggers);
			return { content: [{ type: "text", text: `Skill "${params.name}" created at ${filePath}` }], details: { path: filePath } };
		},
	};
}

// ─── Skill List ───

const SkillListSchema = Type.Object({});

export function createSkillListTool(): AgentTool<typeof SkillListSchema, { count: number }> {
	return {
		name: "skill_list",
		label: "List Skills",
		description: "List all installed skills",
		parameters: SkillListSchema,
		execute: async () => {
			const skills = loadAllSkills();
			if (skills.length === 0) return { content: [{ type: "text", text: "No skills installed." }], details: { count: 0 } };
			const lines = skills.map((s: Skill) => `- ${s.name}: ${s.description}`);
			return { content: [{ type: "text", text: lines.join("\n") }], details: { count: skills.length } };
		},
	};
}

// ─── Skill Delete ───

const SkillDeleteSchema = Type.Object({
	name: Type.String({ description: "Name of the skill to delete" }),
});

export function createSkillDeleteTool(): AgentTool<typeof SkillDeleteSchema, undefined> {
	return {
		name: "skill_delete",
		label: "Delete Skill",
		description: "Delete a skill by name",
		parameters: SkillDeleteSchema,
		execute: async (_id, params, _signal, _onUpdate) => {
			const ok = deleteSkill(params.name);
			return ok
				? { content: [{ type: "text", text: `Skill "${params.name}" deleted.` }], details: undefined }
				: { content: [{ type: "text", text: `Skill "${params.name}" not found.` }], details: undefined };
		},
	};
}

// ─── Soul View ───

const SoulViewSchema = Type.Object({});

export function createSoulViewTool(): AgentTool<typeof SoulViewSchema, { source: string }> {
	return {
		name: "soul_view",
		label: "View SOUL",
		description: "View the current SOUL.md identity file",
		parameters: SoulViewSchema,
		execute: async () => {
			const soul = loadSoul();
			return { content: [{ type: "text", text: soul.identity }], details: { source: soul.source } };
		},
	};
}

// ─── Pass Soul ───

const PassSoulSchema = Type.Object({
	target: Type.String({ description: "Target soul name to pass the conversation to (e.g. 'hanpai', 'zero')" }),
	reason: Type.Optional(Type.String({ description: "Optional reason for passing — helps the target soul understand context" })),
});

export type PassSoulHandler = (target: string, reason?: string) => string;

export function createPassSoulTool(handler: PassSoulHandler): AgentTool<typeof PassSoulSchema> {
	return {
		name: "pass_soul",
		label: "Pass to soul",
		description: "Pass the conversation to another soul. Does NOT produce visible output — use after speak() when you're done and another soul should take the next turn.",
		parameters: PassSoulSchema,
		execute: async (_id, params) => {
			const systemMsg = handler(params.target, params.reason);
			return textResult(systemMsg);
		},
	};
}

// ─── Speak ───

const SpeakSchema = Type.Object({
	soul: Type.String({ description: "Soul to speak (e.g. 'zero', 'hanpai'). Must be a valid group member." }),
	text: Type.String({ description: "The text for this soul to say. Do NOT include [name] tags — they are added automatically." }),
});

export type SpeakHandler = (soul: string, text: string) => string;

export function createSpeakTool(handler: SpeakHandler): AgentTool<typeof SpeakSchema> {
	return {
		name: "speak",
		label: "Make a soul speak",
		description: "CRITICAL: This is your ONLY way to output text. You are a director — never write raw text. Call speak(soul, text) to make a soul say something. The [name] tag is added automatically.",
		parameters: SpeakSchema,
		execute: async (_id, params) => {
			return textResult(handler(params.soul, params.text));
		},
	};
}

export interface GroupChatHandlers {
	onSpeak: SpeakHandler;
}

// ─── Memory Create ───

const MemoryCreateSchema = Type.Object({
	id: Type.String({ description: "Unique memory ID (e.g. '2026-05-22-vim-pref')" }),
	scope: Type.String({ description: "Scope: 'local' (this device) or 'cloud' (synced across devices)" }),
	type: Type.String({ description: "Type: 'user_pref', 'env_fact', 'lesson', or 'episodic'" }),
	priority: Type.Optional(Type.String({ description: "Priority: 'critical', 'high', 'medium' (default), or 'low'" })),
	tags: Type.Optional(Type.Array(Type.String(), { description: "Tags for grouping and search" })),
	content: Type.String({ description: "Memory body (markdown). Be specific — include file paths, command names, exact preferences." }),
});

export function createMemoryCreateTool(store: MemoryStore): AgentTool<typeof MemoryCreateSchema, undefined> {
	return {
		name: "memory_create",
		label: "Create Memory",
		description: "Create a persistent memory. Use when the user shares important facts, preferences, or lessons worth remembering across sessions.",
		parameters: MemoryCreateSchema,
		execute: async (_id, params) => {
			const entry = store.create({
				id: params.id,
				scope: params.scope as MemoryScope,
				type: params.type as MemoryType,
				priority: (params.priority as MemoryPriority) || "medium",
				tags: params.tags || [],
				content: params.content,
				created: new Date().toISOString().slice(0, 10),
				updated: new Date().toISOString().slice(0, 10),
				call_count: 0,
				last_called: null,
			});
			if (!entry) return okText(`Memory "${params.id}" already exists. Use memory_update to modify it.`);
			return okText(`Memory "${params.id}" created (scope: ${params.scope}, priority: ${entry.priority}, weight: ${entry.weight.toFixed(2)})`);
		},
	};
}

// ─── Memory List ───

const MemoryListSchema = Type.Object({
	scope: Type.Optional(Type.String({ description: "Filter by scope: 'local' or 'cloud'" })),
	type: Type.Optional(Type.String({ description: "Filter by type: 'user_pref', 'env_fact', 'lesson', 'episodic'" })),
	priority: Type.Optional(Type.String({ description: "Filter by priority: 'critical', 'high', 'medium', 'low'" })),
});

export function createMemoryListTool(store: MemoryStore): AgentTool<typeof MemoryListSchema, undefined> {
	return {
		name: "memory_list",
		label: "List Memories",
		description: "List all memories, optionally filtered by scope, type, or priority.",
		parameters: MemoryListSchema,
		execute: async (_id, params) => {
			const memories = store.list({
				scope: params.scope as MemoryScope | undefined,
				type: params.type as MemoryType | undefined,
				priority: params.priority as MemoryPriority | undefined,
			});
			if (memories.length === 0) return okText("(no memories)");
			const lines = memories.map((m) =>
				`- [${m.scope}] ${m.id} (${m.type}, ${m.priority}, w=${m.weight.toFixed(2)}, ${m.call_count}c)`
			);
			return okText(`${memories.length} memories:\n${lines.join("\n")}`);
		},
	};
}

// ─── Memory Search ───

const MemorySearchSchema = Type.Object({
	query: Type.String({ description: "Keyword to search for in memory IDs, tags, and content" }),
	scope: Type.Optional(Type.String({ description: "Limit search scope: 'local' or 'cloud' (default: both)" })),
});

export function createMemorySearchTool(store: MemoryStore): AgentTool<typeof MemorySearchSchema, undefined> {
	return {
		name: "memory_search",
		label: "Search Memories",
		description: "Search memories by keyword (case-insensitive, matches id, tags, body).",
		parameters: MemorySearchSchema,
		execute: async (_id, params) => {
			const memories = store.list({
				scope: params.scope as MemoryScope | undefined,
			});
			const q = params.query.toLowerCase();
			const matches = memories.filter((m) =>
				m.id.toLowerCase().includes(q) ||
				m.tags.some((t) => t.toLowerCase().includes(q)) ||
				m.content.toLowerCase().includes(q)
			);
			if (matches.length === 0) return okText(`No memories matching "${params.query}"`);
			const lines = matches.map((m) =>
				`- [${m.scope}] ${m.id} (${m.type}, w=${m.weight.toFixed(2)})\n  ${m.content.slice(0, 120)}${m.content.length > 120 ? "..." : ""}`
			);
			return okText(`${matches.length} matches for "${params.query}":\n${lines.join("\n")}`);
		},
	};
}

// ─── Memory Update ───

const MemoryUpdateSchema = Type.Object({
	id: Type.String({ description: "Memory ID to update" }),
	scope: Type.String({ description: "Memory scope: 'local' or 'cloud'" }),
	content: Type.Optional(Type.String({ description: "New content (if changing)" })),
	priority: Type.Optional(Type.String({ description: "New priority: 'critical', 'high', 'medium', 'low', 'archive'" })),
	type: Type.Optional(Type.String({ description: "New type: 'user_pref', 'env_fact', 'lesson', 'episodic'" })),
	tags: Type.Optional(Type.Array(Type.String(), { description: "New tag list (replaces existing tags)" })),
});

export function createMemoryUpdateTool(store: MemoryStore): AgentTool<typeof MemoryUpdateSchema, undefined> {
	return {
		name: "memory_update",
		label: "Update Memory",
		description: "Update a memory's content, priority, type, or tags.",
		parameters: MemoryUpdateSchema,
		execute: async (_id, params) => {
			const patch: Record<string, unknown> = {};
			if (params.content !== undefined) patch.content = params.content;
			if (params.priority !== undefined) patch.priority = params.priority;
			if (params.type !== undefined) patch.type = params.type;
			if (params.tags !== undefined) patch.tags = params.tags;

			const updated = store.update(params.id, params.scope as MemoryScope, patch as Parameters<MemoryStore["update"]>[2]);
			if (!updated) return okText(`Memory "${params.id}" (${params.scope}) not found.`);
			return okText(`Memory "${params.id}" updated. New weight: ${updated.weight.toFixed(2)}`);
		},
	};
}

// ─── Memory Delete ───

const MemoryDeleteSchema = Type.Object({
	id: Type.String({ description: "Memory ID to delete" }),
	scope: Type.String({ description: "Memory scope: 'local' or 'cloud'" }),
});

export function createMemoryDeleteTool(store: MemoryStore): AgentTool<typeof MemoryDeleteSchema, undefined> {
	return {
		name: "memory_delete",
		label: "Delete Memory",
		description: "Delete a memory permanently. Use cautiously — only for obsolete or incorrect memories.",
		parameters: MemoryDeleteSchema,
		execute: async (_id, params) => {
			const ok = store.delete(params.id, params.scope as MemoryScope);
			if (!ok) return okText(`Memory "${params.id}" (${params.scope}) not found.`);
			return okText(`Memory "${params.id}" deleted.`);
		},
	};
}

// ─── Factory ───

export function createHalfPiTools(cwd: string, handlers?: GroupChatHandlers): Map<ToolName, AgentTool> {
	const tools = new Map<ToolName, AgentTool>();
	tools.set("read", createReadTool(cwd));
	tools.set("bash", createBashTool(cwd));
	tools.set("edit", createEditTool(cwd));
	tools.set("write", createWriteTool(cwd));
	tools.set("grep", createGrepTool(cwd));
	tools.set("find", createFindTool(cwd));
	tools.set("ls", createLsTool(cwd));
	tools.set("skill_create", createSkillCreateTool());
	tools.set("skill_list", createSkillListTool());
	tools.set("skill_delete", createSkillDeleteTool());
	tools.set("soul_view", createSoulViewTool());
	if (handlers?.onSpeak) {
		tools.set("speak", createSpeakTool(handlers.onSpeak));
	}
	// Memory tools
	const memoryStore = new MemoryStore();
	tools.set("memory_create", createMemoryCreateTool(memoryStore));
	tools.set("memory_list", createMemoryListTool(memoryStore));
	tools.set("memory_search", createMemorySearchTool(memoryStore));
	tools.set("memory_update", createMemoryUpdateTool(memoryStore));
	tools.set("memory_delete", createMemoryDeleteTool(memoryStore));
	return tools;
}
