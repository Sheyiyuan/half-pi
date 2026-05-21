/**
 * SOUL.md loader.
 *
 * SOUL.md is the permanent identity file for half-pi. It defines who the agent
 * is, its personality, speaking style, and hard rules. It is loaded from
 * ~/.half-pi/SOUL.md and injected as the first slot in the system prompt.
 *
 * If the file does not exist, a built-in default identity is used.
 */

import { existsSync, readFileSync } from "node:fs";
import { getSoulPath } from "../config.ts";

/** Default identity used when SOUL.md is not present. */
const DEFAULT_SOUL = `You are half-pi, a self-improving coding agent. You help users by reading files,
executing commands, editing code, and writing new files. You are direct,
concise, and proactive — you take initiative on tasks but stay humble.
You can also update your own skills and knowledge to improve over time.`;

export interface Soul {
	/** Full content of SOUL.md, or default if not found */
	content: string;
	/** Where the soul was loaded from ("file" or "builtin") */
	source: "file" | "builtin";
}

/**
 * Load the SOUL from ~/.half-pi/SOUL.md.
 * Falls back to DEFAULT_SOUL if the file does not exist.
 */
export function loadSoul(): Soul {
	const soulPath = getSoulPath();
	if (existsSync(soulPath)) {
		return {
			content: readFileSync(soulPath, "utf-8").trim(),
			source: "file",
		};
	}
	return {
		content: DEFAULT_SOUL,
		source: "builtin",
	};
}
