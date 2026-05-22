/**
 * System prompt construction — new layered design.
 *
 * Prompt buildup (in order):
 *   1. Core commitment (core.SOUL.md) — thin, shared by all souls
 *   2. Soul identity (souls/<name>/identity.md) — this soul's personality
 *   3. Group info (if multi-soul) — list of members
 *   4. Tools
 *   5. Guidelines
 *   6. Skills
 *   7. Context (date, cwd)
 *
 * Backward compatible: when no group is specified, behaves like old
 * single-soul mode but loads from the new structure.
 */

import { formatSkillsForPrompt, type Skill } from "./skills.js";
import { loadSoul, loadCoreSoul } from "./soul-loader.js";
import { loadGroup, type GroupConfig } from "./groups.js";
import { getStylePath } from "../config.js";
import { existsSync, readFileSync } from "node:fs";

export interface BuildSystemPromptOptions {
	/** Override soul identity (for testing or --system-prompt flag) */
	customPrompt?: string;
	/** Tools to include */
	selectedTools?: string[];
	/** One-line tool descriptions */
	toolSnippets?: Record<string, string>;
	/** Additional guidelines */
	promptGuidelines?: string[];
	/** Working directory */
	cwd: string;
	/** Pre-loaded skills */
	skills?: Skill[];
	/**
	 * Group name to load (e.g. "daily").
	 * If provided, loads all souls in the group into the prompt.
	 * If omitted, falls back to single-soul mode (loads default soul).
	 */
	groupName?: string;
	/**
	 * Soul names for this session (overrides group config).
	 * For Phase 1: typically just ["zero"].
	 */
	soulNames?: string[];
	/** Current active soul (for group chat mode). */
	currentSoul?: string;
	/** Pre-formatted memory section to inject (from MemoryInjector). */
	injectedMemory?: string;
}

function buildGroupChatPrompt(
	souls: { name: string; identity: string }[],
	coreCommitment: string,
	defaultSoul: string,
	currentSoul: string,
	promptRules?: string[],
): string {
	const parts: string[] = [];

	// Core commitment (if exists)
	if (coreCommitment) {
		parts.push(coreCommitment);
		parts.push("");
	}

	// Global style file (optional, ~/.half-pi/style.md)
	const stylePath = getStylePath();
	let styleContent = "";
	if (existsSync(stylePath)) {
		try {
			styleContent = readFileSync(stylePath, "utf-8").trim();
		} catch { /* ignore */ }
	}

	// ─── Main task ───
	parts.push("<main_task>");
	parts.push("You are a conversation director. You control the souls in this group.");
	parts.push("You do NOT speak as any soul directly. To make a soul talk, use the speak(soul, text) tool.");
	parts.push("This is your ONLY way to output text. Never write raw text outside of speak().");
	if (souls.length > 1) {
		parts.push("You can call speak() multiple times per turn for a natural back-and-forth.");
		parts.push("Don't alternate mechanically. Natural conversation has varied rhythm.");
		parts.push("If the user mentions a soul by name, make that soul speak.");
	}
	parts.push("</main_task>");
	parts.push("");

	// ─── Director config ───
	parts.push("<director_config>");
	parts.push(`Group members: ${souls.map((s) => s.name).join(", ")}`);
	parts.push(`Available output method: speak(soul, text) where soul ∈ {${souls.map((s) => s.name).join(", ")}}`);
	parts.push("After ~3 soul switches, the tool pauses and waits for user input.");
	parts.push("Other tools (bash, read, edit, write, grep, find, ls) are for executing tasks.");
	parts.push("</director_config>");
	parts.push("");

	// ─── Identities ───
	parts.push("<identities>");
	for (const soul of souls) {
		parts.push("");
		parts.push(`=== ${soul.name} ===`);
		parts.push(soul.identity);
	}
	parts.push("</identities>");
	parts.push("");

	// ─── Output format ───
	parts.push("<output_format>");
	parts.push("[soul_name] tags are added automatically by the system. Do NOT include them in your text.");
	parts.push("User messages appear as: [user] <text>");
	parts.push("System messages appear as: [system] <text>");
	parts.push("Your speak() calls appear as: [soul] <text>");
	parts.push("</output_format>");

	// Custom user-defined rules
	if (promptRules && promptRules.length > 0) {
		parts.push("");
		parts.push("<custom_rules>");
		for (const rule of promptRules) {
			parts.push(`- ${rule}`);
		}
		parts.push("</custom_rules>");
	}

	// Global style
	if (styleContent) {
		parts.push("");
		parts.push(styleContent);
	}

	return parts.join("\n");
}

export function buildSystemPrompt(options: BuildSystemPromptOptions): string {
	const {
		customPrompt,
		selectedTools,
		toolSnippets,
		promptGuidelines,
		cwd,
		skills: providedSkills,
		groupName,
		soulNames: explicitSoulNames,
		currentSoul,
	} = options;

	const now = new Date();
	const date = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;

	let prompt: string;

	if (customPrompt) {
		// Custom prompt bypasses all soul/group loading
		prompt = customPrompt;
	} else if (groupName || (explicitSoulNames && explicitSoulNames.length > 0)) {
		// Group mode: load group config + souls
		let souls: string[];
		let defaultSoul: string;

		if (explicitSoulNames && explicitSoulNames.length > 0) {
			souls = explicitSoulNames;
			defaultSoul = souls[0];
		} else {
			const group = loadGroup(groupName!);
			if (group) {
				souls = group.souls;
				defaultSoul = group.defaultSoul ?? souls[0];
			} else {
				// Group not found, fall back to single soul
				souls = [groupName!];
				defaultSoul = groupName!;
			}
		}

		// Load all soul identities
		const loaded = souls.map((name) => ({
			name,
			identity: loadSoul(name).identity,
		}));

		// Load core commitment
		const coreSoul = loadSoul(defaultSoul);

		// Load group config for custom rules
		let promptRules: string[] | undefined;
		if (groupName) {
			const group = loadGroup(groupName);
			if (group) promptRules = group.promptRules;
		}

		prompt = buildGroupChatPrompt(loaded, coreSoul.core, defaultSoul, currentSoul || defaultSoul, promptRules);
	} else {
		// Legacy/single-soul mode: load default soul
		const soul = loadSoul();
		prompt = `## Your identity\n\n${soul.core ? `${soul.core}\n\n` : ""}${soul.identity}`;
		if (soul.source !== "builtin") {
			prompt += `\n\n[source: ${soul.source}]`;
		}
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

	// --- Memory ---
	if (options.injectedMemory) {
		prompt += `\n\n${options.injectedMemory}`;
	}

	return prompt;
}
