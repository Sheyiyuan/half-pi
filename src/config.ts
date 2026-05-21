/**
 * Paths and configuration for half-pi.
 */

import { homedir } from "node:os";
import { join } from "node:path";

/** half-pi data directory: ~/.half-pi/ */
export function getHalfPiDir(): string {
	return join(homedir(), ".half-pi");
}

/** SOUL.md path */
export function getSoulPath(): string {
	return join(getHalfPiDir(), "SOUL.md");
}

/** Skills directory */
export function getSkillsDir(): string {
	return join(getHalfPiDir(), "skills");
}

/** Memory directory */
export function getMemoryDir(): string {
	return join(getHalfPiDir(), "memory");
}

/** Sessions directory */
export function getSessionsDir(): string {
	return join(getHalfPiDir(), "sessions");
}
