/**
 * System prompt construction with SOUL.md as the first identity slot.
 *
 * Based on pi's system-prompt.ts, but simplified:
 * - SOUL.md is always the first section
 * - No pi-specific docs (those are pi's identity, not half-pi's)
 * - Skill injection follows the same pattern
 */

import { formatSkillsForPrompt, type Skill } from "./skills.ts";
import { loadSoul } from "./soul-loader.ts";

export interface BuildSystemPromptOptions {
	/** Override SOUL.md identity (for testing or --system-prompt flag) */
	customPrompt?: string;
	/** Tools to include. Default: [read, bash, edit, write, grep, find, ls] */
	selectedTools?: string[];
	/** One-line tool descriptions */
	toolSnippets?: Record<string, string>;
	/** Additional guidelines appended after built-in ones */
	promptGuidelines?: string[];
	/** Working directory */
	cwd: string;
	/** Pre-loaded skills */
	skills?: Skill[];
}

export function buildSystemPrompt(options: BuildSystemPromptOptions): string {
	const {
		customPrompt,
		selectedTools,
		toolSnippets,
		promptGuidelines,
		cwd,
		skills: providedSkills,
	} = options;

	const now = new Date();
	const date = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
	const promptCwd = cwd.replace(/\\/g, "/");

	// --- Identity: SOUL.md or custom prompt ---
	let prompt: string;
	if (customPrompt) {
		prompt = customPrompt;
	} else {
		const soul = loadSoul();
		prompt = `${soul.content}\n`;
	}

	// --- Tools section ---
	const tools = selectedTools ?? ["read", "bash", "edit", "write", "grep", "find", "ls"];
	const visibleTools = tools.filter((name) => !!toolSnippets?.[name]);
	const toolsList =
		visibleTools.length > 0
			? visibleTools.map((name) => `- ${name}: ${toolSnippets![name]}`).join("\n")
			: "(none)";

	prompt += `\n\n## Available tools\n\n${toolsList}`;

	// --- Guidelines ---
	const guidelinesList: string[] = [];

	// File exploration guidelines (same logic as pi)
	const hasBash = tools.includes("bash");
	const hasGrep = tools.includes("grep");
	const hasFind = tools.includes("find");
	const hasLs = tools.includes("ls");

	if (hasBash && (hasGrep || hasFind || hasLs)) {
		guidelinesList.push("Prefer grep/find/ls tools over bash for file exploration (faster, respects .gitignore)");
	}

	if (promptGuidelines) {
		for (const g of promptGuidelines) {
			const trimmed = g.trim();
			if (trimmed.length > 0) guidelinesList.push(trimmed);
		}
	}

	guidelinesList.push("Be concise in your responses");
	guidelinesList.push("Show file paths clearly when working with files");

	const guidelines = guidelinesList.map((g) => `- ${g}`).join("\n");
	prompt += `\n\n## Guidelines\n\n${guidelines}`;

	// --- Skills ---
	const hasRead = tools.includes("read");
	const skills = providedSkills ?? [];
	if (hasRead && skills.length > 0) {
		prompt += formatSkillsForPrompt(skills);
	}

	// --- Context ---
	prompt += `\n\nCurrent date: ${date}`;
	prompt += `\nCurrent working directory: ${promptCwd}`;

	return prompt;
}
