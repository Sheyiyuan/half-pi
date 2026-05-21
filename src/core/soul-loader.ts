/**
 * SOUL.md loader — new layered design.
 *
 * Two layers:
 *   1. core.SOUL.md — thin core commitment, shared by all souls
 *   2. souls/<name>/identity.md — the soul's identity (personality, rules)
 *
 * Backward compatible: if new structure doesn't exist, falls back to
 * legacy ~/.half-pi/SOUL.md → builtin default.
 *
 * Data contract: name == directory name under souls/.
 *   e.g. name="zolo" → souls/zolo/identity.md
 *   e.g. name="zero" → souls/zero/identity.md
 */
import { existsSync, readFileSync } from "node:fs";
import {
	getCoreSoulPath,
	getSoulIdentityPath,
	getSoulPath,
} from "../config.js";

/** Default identity used when no soul file is found. */
const DEFAULT_SOUL = `You are half-pi, a self-improving coding agent. You help users by reading files,
executing commands, editing code, and writing new files. You are direct,
concise, and proactive — you take initiative on tasks but stay humble.
You can also update your own skills and knowledge to improve over time.`;

export interface SoulLoadResult {
	/** Core commitment content (from core.SOUL.md, or empty if not found) */
	core: string;
	/** Identity content (from souls/<name>/identity.md, legacy SOUL.md, or builtin) */
	identity: string;
	/** Source description for display */
	source: string;
}

/**
 * Load the core commitment from core.SOUL.md.
 * Returns empty string if not found (graceful degradation).
 */
export function loadCoreSoul(): string {
	const path = getCoreSoulPath();
	if (existsSync(path)) {
		return readFileSync(path, "utf-8").trim();
	}
	return "";
}

/**
 * Load identity for a named soul.
 *
 * Resolution order (first wins):
 *   1. souls/<name>/identity.md
 *   2. legacy ~/.half-pi/SOUL.md
 *   3. builtin default
 */
export function loadSoulIdentity(name: string): { identity: string; source: string } {
	const identityPath = getSoulIdentityPath(name);
	if (existsSync(identityPath)) {
		return {
			identity: readFileSync(identityPath, "utf-8").trim(),
			source: `souls/${name}/identity.md`,
		};
	}

	// Fallback: legacy SOUL.md
	const legacyPath = getSoulPath();
	if (existsSync(legacyPath)) {
		return {
			identity: readFileSync(legacyPath, "utf-8").trim(),
			source: "~/.half-pi/SOUL.md (legacy)",
		};
	}

	return {
		identity: DEFAULT_SOUL,
		source: "builtin",
	};
}

/**
 * Full load for a named soul: core commitment + identity.
 * This is the main API for Phase 1.
 */
export function loadSoul(name: string = "zero"): SoulLoadResult {
	return {
		core: loadCoreSoul(),
		...loadSoulIdentity(name),
	};
}
