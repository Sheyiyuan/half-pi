/**
 * Group configuration — parses groups/<name>.json.
 *
 * Format:
 * ```json
 * {
 *   "name": "日常",
 *   "souls": ["zero", "hanpai"],
 *   "dispatch": "default",
 *   "default_soul": "zero"
 * ```
 *
 * Data contract: souls names == directory names under souls/.
 *   e.g. "hanpai" → souls/hanpai/identity.md
 */

import { existsSync, readFileSync, readdirSync } from "node:fs";
import { getGroupsDir, getGroupConfigPath } from "../config.js";

export type DispatchMode = "default" | "weighted" | "auto";

export interface GroupConfig {
	/** Human-readable group name */
	name: string;
	/** Ordered list of soul names in this group */
	souls: string[];
	/** How to dispatch user messages to souls */
	dispatch: DispatchMode;
	/** Default soul (required when dispatch=default) */
	defaultSoul: string;
	/** Custom prompt rules appended to system prompt (user-defined behavior, tone, style) */
	promptRules?: string[];
}

/**
 * Load a group configuration by name.
 * Returns null if the file doesn't exist or is malformed.
 */
export function loadGroup(name: string): GroupConfig | null {
	const path = getGroupConfigPath(name);
	if (!existsSync(path)) return null;

	try {
		const raw = readFileSync(path, "utf-8");
		const parsed = JSON.parse(raw) as Record<string, unknown>;

		const souls = parsed.souls;
		if (!Array.isArray(souls) || souls.length === 0) return null;

		const soulNames = souls.filter((s): s is string => typeof s === "string");
		if (soulNames.length === 0) return null;

		const dispatchRaw = typeof parsed.dispatch === "string" ? parsed.dispatch : "default";
		let dispatch: DispatchMode = "default";
		if (dispatchRaw === "weighted" || dispatchRaw === "auto") {
			dispatch = dispatchRaw;
		}

		const promptRulesRaw = parsed.prompt_rules;
		const promptRules = Array.isArray(promptRulesRaw)
			? promptRulesRaw.filter((r): r is string => typeof r === "string")
			: undefined;

		return {
			name: typeof parsed.name === "string" ? parsed.name : name,
			souls: soulNames,
			dispatch,
			defaultSoul: typeof parsed.default_soul === "string" ? parsed.default_soul : soulNames[0],
			promptRules,
		};
	} catch {
		return null;
	}
}

/**
 * List all available groups (from filenames in groups/ directory).
 */
export function listGroups(): string[] {
	const dir = getGroupsDir();
	if (!existsSync(dir)) return [];

	try {
		const files = readdirSync(dir);
		return files
			.filter((f) => f.endsWith(".json"))
			.map((f) => f.replace(/\.json$/, ""));
	} catch {
		return [];
	}
}
