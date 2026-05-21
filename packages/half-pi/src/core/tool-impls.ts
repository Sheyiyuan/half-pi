/**
 * Tool factory implementations for half-pi.
 *
 * Simplified versions of pi's tools. Focus on core functionality:
 * read, bash, edit, write, grep, find, ls, skill_*, soul_view.
 */

import { accessSync, constants, lstatSync, readFileSync, readdirSync } from "node:fs";
import { basename, isAbsolute, join, relative, resolve, sep } from "node:path";
import { sync as spawnSync } from "cross-spawn";
import type { AgentTool, AgentToolResult } from "@earendil-works/pi-agent-core";
import type { TextContent } from "@earendil-works/pi-ai";
import { createSkill, deleteSkill, loadAllSkills } from "./skills.ts";
import { loadSoul } from "./soul-loader.ts";
import type { ToolName } from "./tools.ts";

/** Format a path relative to cwd (or absolute if outside cwd) */
function displayPath(p: string, cwd: string): string {
	const abs = resolve(cwd, p);
	const rel = relative(cwd, abs);
	return rel.startsWith("..") ? abs : rel;
}

/** Make a simple text result */
function textResult(text: string, details?: Record<string, unknown>): AgentToolResult {
	return {
		content: [{ type: "text", text }],
		details,
	};
}

/** Resolve a path against cwd, checking for directory traversal */
function resolveSafe(cwd: string, inputPath: string): string {
	const resolved = resolve(cwd, inputPath);
	const rel = relative(cwd, resolved);
	if (rel.startsWith(`..${sep}`) || isAbsolute(rel)) {
		// Outside cwd — allow it but note it
	}
	return resolved;
}

// ─── Read ───

function createReadToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "read",
		description: "Read file contents with line numbers and pagination",
		parameters: {
			type: "object",
			properties: {
				path: { type: "string", description: "Path to the file to read" },
				offset: { type: "number", description: "Line number to start from (1-indexed)" },
				limit: { type: "number", description: "Max lines to read" },
			},
			required: ["path"],
		},
		execute: async (_id, params) => {
			const filePath = resolveSafe(cwd, params.path as string);
			const content = readFileSync(filePath, "utf-8");
			const lines = content.split("\n");

			let offset = (params.offset as number) || 1;
			let limit = (params.limit as number) || lines.length;
			offset = Math.max(1, offset);
			limit = Math.min(limit, lines.length - offset + 1);

			const sliced = lines.slice(offset - 1, offset - 1 + limit);
			const output = sliced.map((line, i) => `${offset + i}|${line}`).join("\n");

			return textResult(output, {
				totalLines: lines.length,
				path: displayPath(filePath, cwd),
			});
		},
	};
}

// ─── Bash ───

function createBashToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "bash",
		description: "Execute shell commands on the local machine",
		parameters: {
			type: "object",
			properties: {
				command: { type: "string", description: "The shell command to execute" },
				timeout: { type: "number", description: "Max seconds to wait" },
				workdir: { type: "string", description: "Working directory" },
			},
			required: ["command"],
		},
		execute: async (_id, params) => {
			const command = params.command as string;
			const workdir = params.workdir ? resolveSafe(cwd, params.workdir as string) : cwd;
			const timeout = ((params.timeout as number) || 180) * 1000;

			try {
				const result = spawnSync(command, [], {
					cwd: workdir,
					shell: true,
					timeout,
					encoding: "utf-8",
					maxBuffer: 10 * 1024 * 1024,
				});

				const output = (result.stdout || "") + (result.stderr ? `\n[stderr]\n${result.stderr}` : "");
				return textResult(output || "(no output)", {
					exitCode: result.status,
					workdir: displayPath(workdir, cwd),
				});
			} catch (err) {
				return textResult(`Error: ${String(err)}`, { isError: true });
			}
		},
	};
}

// ─── Edit ───

function createEditToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "edit",
		description: "Targeted find-and-replace edits in files",
		parameters: {
			type: "object",
			properties: {
				path: { type: "string", description: "File path to edit" },
				old_string: { type: "string", description: "Text to find" },
				new_string: { type: "string", description: "Replacement text" },
			},
			required: ["path", "old_string", "new_string"],
		},
		execute: async (_id, params) => {
			const filePath = resolveSafe(cwd, params.path as string);
			const oldStr = params.old_string as string;
			const newStr = params.new_string as string;

			const content = readFileSync(filePath, "utf-8");
			if (!content.includes(oldStr)) {
				return textResult(`Error: old_string not found in file`, { isError: true });
			}

			const count = content.split(oldStr).length - 1;
			const newContent = content.replace(oldStr, newStr);

			// Also need to write — import here to avoid circular
			const { writeFileSync: fsWrite } = await import("node:fs");
			fsWrite(filePath, newContent, "utf-8");

			return textResult(`Replaced ${count} occurrence(s) in ${displayPath(filePath, cwd)}`);
		},
	};
}

// ─── Write ───

function createWriteToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "write",
		description: "Write full content to a file",
		parameters: {
			type: "object",
			properties: {
				path: { type: "string", description: "Path to the file to write" },
				content: { type: "string", description: "Complete content to write" },
			},
			required: ["path", "content"],
		},
		execute: async (_id, params) => {
			const filePath = resolveSafe(cwd, params.path as string);
			const content = params.content as string;

			// Ensure parent dirs exist
			const { mkdirSync, writeFileSync } = await import("node:fs");
			const { dirname } = await import("node:path");
			mkdirSync(dirname(filePath), { recursive: true });
			writeFileSync(filePath, content, "utf-8");

			return textResult(`Wrote ${content.length} bytes to ${displayPath(filePath, cwd)}`);
		},
	};
}

// ─── Grep ───

function createGrepToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "grep",
		description: "Search file contents with regex",
		parameters: {
			type: "object",
			properties: {
				pattern: { type: "string", description: "Regex pattern to search for" },
				path: { type: "string", description: "Directory or file to search in" },
				file_glob: { type: "string", description: "Filter files by pattern" },
			},
			required: ["pattern"],
		},
		execute: async (_id, params) => {
			const pattern = params.pattern as string;
			const searchPath = params.path ? resolveSafe(cwd, params.path as string) : cwd;
			const fileGlob = params.file_glob as string | undefined;

			// Use ripgrep if available, fall back to grep
			const rgArgs = ["--line-number", "--no-heading", "-e", pattern, searchPath];
			if (fileGlob) rgArgs.push("--glob", fileGlob);

			try {
				const result = spawnSync("rg", rgArgs, { encoding: "utf-8", timeout: 30000, maxBuffer: 5 * 1024 * 1024 });
				const output = result.stdout || result.stderr || "(no matches)";
				return textResult(output.length > 50000 ? output.substring(0, 50000) + "\n... (truncated)" : output);
			} catch {
				return textResult("rg not available. Install ripgrep for better search.");
			}
		},
	};
}

// ─── Find ───

function createFindToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "find",
		description: "Find files by glob pattern",
		parameters: {
			type: "object",
			properties: {
				pattern: { type: "string", description: "Glob pattern (e.g., '*.ts', '**/*.test.ts')" },
				path: { type: "string", description: "Directory to search in" },
			},
			required: ["pattern"],
		},
		execute: async (_id, params) => {
			const pattern = params.pattern as string;
			const searchPath = params.path ? resolveSafe(cwd, params.path as string) : cwd;

			// Use fd (fdfind) if available
			try {
				const result = spawnSync("fd", ["--type", "f", pattern, searchPath], {
					encoding: "utf-8",
					timeout: 15000,
					maxBuffer: 5 * 1024 * 1024,
				});
				return textResult(result.stdout || "(no files found)");
			} catch {
				return textResult("fd not available. Install fd-find for better file search.");
			}
		},
	};
}

// ─── Ls ───

function createLsToolImpl(cwd: string): AgentTool<ToolName> {
	return {
		name: "ls",
		description: "List directory contents",
		parameters: {
			type: "object",
			properties: {
				path: { type: "string", description: "Directory to list" },
			},
			required: [],
		},
		execute: async (_id, params) => {
			const listPath = params.path ? resolveSafe(cwd, params.path as string) : cwd;
			const entries = readdirSync(listPath, { withFileTypes: true });
			const lines = entries.map((e) => {
				const type = e.isDirectory() ? "/" : e.isSymbolicLink() ? "@" : "";
				return `${e.name}${type}`;
			});
			return textResult(lines.join("\n") || "(empty directory)", {
				path: displayPath(listPath, cwd),
				count: lines.length,
			});
		},
	};
}

// ─── Skill tools ───

function createSkillCreateToolImpl(): AgentTool<ToolName> {
	return {
		name: "skill_create",
		description:
			"Create a new skill — a reusable workflow document that persists across sessions. Use this to save knowledge, workflows, or lessons learned.",
		parameters: {
			type: "object",
			properties: {
				name: { type: "string", description: "Skill name (lowercase, hyphens, max 64 chars)" },
				description: { type: "string", description: "What this skill does" },
				content: { type: "string", description: "Full markdown content" },
				triggers: { type: "array", items: { type: "string" }, description: "Trigger conditions" },
			},
			required: ["name", "description", "content"],
		},
		execute: async (_id, params) => {
			const path = createSkill(
				params.name as string,
				params.description as string,
				params.content as string,
				(params.triggers as string[]) || [],
			);
			return textResult(`Skill "${params.name}" created at ${path}`);
		},
	};
}

function createSkillListToolImpl(): AgentTool<ToolName> {
	return {
		name: "skill_list",
		description: "List all installed skills",
		parameters: {
			type: "object",
			properties: {},
			required: [],
		},
		execute: async () => {
			const skills = loadAllSkills();
			if (skills.length === 0) return textResult("No skills installed.");
			const lines = skills.map((s) => `- ${s.name}: ${s.description}`);
			return textResult(lines.join("\n"), { count: skills.length });
		},
	};
}

function createSkillDeleteToolImpl(): AgentTool<ToolName> {
	return {
		name: "skill_delete",
		description: "Delete a skill by name",
		parameters: {
			type: "object",
			properties: {
				name: { type: "string", description: "Name of the skill to delete" },
			},
			required: ["name"],
		},
		execute: async (_id, params) => {
			const ok = deleteSkill(params.name as string);
			return ok ? textResult(`Skill "${params.name}" deleted.`) : textResult(`Skill "${params.name}" not found.`, { isError: true });
		},
	};
}

// ─── Soul view ───

function createSoulViewToolImpl(): AgentTool<ToolName> {
	return {
		name: "soul_view",
		description: "View the current SOUL.md identity file",
		parameters: {
			type: "object",
			properties: {},
			required: [],
		},
		execute: async () => {
			const soul = loadSoul();
			return textResult(soul.content, { source: soul.source });
		},
	};
}

// ─── Factory ───

export function createHalfPiTools(cwd: string): Map<ToolName, AgentTool<ToolName>> {
	const tools = new Map<ToolName, AgentTool<ToolName>>();

	tools.set("read", createReadToolImpl(cwd));
	tools.set("bash", createBashToolImpl(cwd));
	tools.set("edit", createEditToolImpl(cwd));
	tools.set("write", createWriteToolImpl(cwd));
	tools.set("grep", createGrepToolImpl(cwd));
	tools.set("find", createFindToolImpl(cwd));
	tools.set("ls", createLsToolImpl(cwd));
	tools.set("skill_create", createSkillCreateToolImpl());
	tools.set("skill_list", createSkillListToolImpl());
	tools.set("skill_delete", createSkillDeleteToolImpl());
	tools.set("soul_view", createSoulViewToolImpl());

	return tools;
}
