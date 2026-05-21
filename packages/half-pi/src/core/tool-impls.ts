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
import { createSkill, deleteSkill, loadAllSkills } from "./skills.ts";
import { loadSoul } from "./soul-loader.ts";
import type { ToolName } from "./tools.ts";

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
	pattern: Type.String({ description: "Glob pattern for file name matching (e.g. '*.ts', '**/*.test.ts')" }),
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
			const lines = skills.map((s) => `- ${s.name}: ${s.description}`);
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
			return { content: [{ type: "text", text: soul.content }], details: { source: soul.source } };
		},
	};
}

// ─── Factory ───

export function createHalfPiTools(cwd: string): Map<ToolName, AgentTool> {
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
	return tools;
}
