/**
 * Skill management for half-pi.
 *
 * Skills are reusable knowledge documents (markdown with YAML frontmatter)
 * that are loaded on-demand and injected into the system prompt.
 *
 * Based on pi's skills.ts but extended with write capability (CRUD).
 */

import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync, unlinkSync } from "node:fs";
import { basename, join } from "node:path";
import { getSkillsDir } from "../config.ts";

export interface Skill {
	name: string;
	description: string;
	/** Trigger conditions — when to auto-load this skill */
	triggers?: string[];
	/** Full markdown content (without frontmatter) */
	content: string;
	/** File path on disk */
	path: string;
}

/**
 * Parse YAML frontmatter from markdown content.
 * Returns { metadata, body } where metadata is key-value pairs and body
 * is the content after the `---` fence.
 */
function parseFrontmatter(raw: string): { metadata: Record<string, string>; body: string } {
	const trimmed = raw.trim();
	if (!trimmed.startsWith("---")) {
		return { metadata: {}, body: trimmed };
	}

	const endIdx = trimmed.indexOf("---", 3);
	if (endIdx === -1) {
		return { metadata: {}, body: trimmed };
	}

	const fmBlock = trimmed.substring(3, endIdx).trim();
	const body = trimmed.substring(endIdx + 3).trim();

	const metadata: Record<string, string> = {};
	for (const line of fmBlock.split("\n")) {
		const colonIdx = line.indexOf(":");
		if (colonIdx > 0) {
			const key = line.substring(0, colonIdx).trim();
			const value = line.substring(colonIdx + 1).trim();
			metadata[key] = value;
		}
	}

	return { metadata, body };
}

/** Load all skills from the skills directory */
export function loadAllSkills(): Skill[] {
	const dir = getSkillsDir();
	if (!existsSync(dir)) return [];

	const skills: Skill[] = [];
	try {
		for (const entry of readdirSync(dir)) {
			if (!entry.endsWith(".md")) continue;
			const path = join(dir, entry);
			const raw = readFileSync(path, "utf-8");
			const { metadata, body } = parseFrontmatter(raw);
			skills.push({
				name: metadata.name || basename(entry, ".md"),
				description: metadata.description || "",
				triggers: metadata.triggers ? metadata.triggers.split(",").map((s) => s.trim()) : undefined,
				content: body,
				path,
			});
		}
	} catch {
		// If directory can't be read, return empty
	}
	return skills;
}

/** Format skills for inclusion in a system prompt */
export function formatSkillsForPrompt(skills: Skill[]): string {
	if (skills.length === 0) return "";

	let result = "\n\n<available_skills>\n";
	for (const skill of skills) {
		result += `  - ${skill.name}: ${skill.description}\n`;
	}
	result += "</available_skills>";

	return result;
}

/** Create a new skill file */
export function createSkill(name: string, description: string, content: string, triggers?: string[]): string {
	const dir = getSkillsDir();
	if (!existsSync(dir)) {
		mkdirSync(dir, { recursive: true });
	}

	const frontmatter = [
		"---",
		`name: ${name}`,
		`description: ${description}`,
		triggers && triggers.length > 0 ? `triggers: ${triggers.join(", ")}` : null,
		"---",
	]
		.filter(Boolean)
		.join("\n");

	const fullContent = `${frontmatter}\n\n${content}`;
	const path = join(dir, `${name.replace(/[^a-zA-Z0-9_-]/g, "-").toLowerCase()}.md`);
	writeFileSync(path, fullContent, "utf-8");
	return path;
}

/** Delete a skill by name */
export function deleteSkill(name: string): boolean {
	const dir = getSkillsDir();
	const path = join(dir, `${name}.md`);
	if (!existsSync(path)) return false;
	unlinkSync(path);
	return true;
}
