/**
 * Memory store — Markdown + YAML frontmatter persistence.
 *
 * Directory structure:
 *   ~/.half-pi/memory/
 *   ├── local/    ← device-bound, not synced via git
 *   └── cloud/    ← cross-device, git-synced
 *
 * Each memory is a .md file with YAML frontmatter.
 * Weight formula: clamp(base_weight × decay + freq_bonus, 0, 1)
 */

import { existsSync, mkdirSync, readFileSync, writeFileSync, readdirSync, unlinkSync } from "node:fs";
import { join, extname } from "node:path";
import { getMemoryDir } from "../config.js";

// ─── Types ───

export type MemoryScope = "local" | "cloud";
export type MemoryType = "user_pref" | "env_fact" | "lesson" | "episodic";
export type MemoryPriority = "critical" | "high" | "medium" | "low" | "archive";

export interface MemoryMeta {
	id: string;
	scope: MemoryScope;
	type: MemoryType;
	priority: MemoryPriority;
	tags: string[];
	created: string;  // YYYY-MM-DD
	updated: string;
	call_count: number;
	last_called: string | null;  // ISO datetime or null
	weight: number;
}

export interface MemoryEntry extends MemoryMeta {
	content: string;  // body after frontmatter
}

// ─── Weight calculation ───

const PRIORITY_WEIGHTS: Record<MemoryPriority, number> = {
	critical: 1.0,
	high: 0.8,
	medium: 0.5,
	low: 0.2,
	archive: 0.0,
};

function decayFactor(lastCalled: string | null): number {
	if (!lastCalled) return 1.0; // never called → no decay
	const days = (Date.now() - new Date(lastCalled).getTime()) / (1000 * 60 * 60 * 24);
	if (days < 7) return 1.0;
	if (days < 30) return 0.9;
	if (days < 90) return 0.7;
	return 0.5;
}

function freqBonus(callCount: number): number {
	if (callCount <= 5) return 0;
	if (callCount <= 20) return 0.05;
	if (callCount <= 50) return 0.10;
	return 0.15;
}

export function calculateWeight(meta: MemoryMeta): number {
	const base = PRIORITY_WEIGHTS[meta.priority];
	const decay = decayFactor(meta.last_called);
	const bonus = freqBonus(meta.call_count);
	return Math.max(0, Math.min(1, base * decay + bonus));
}

// ─── YAML frontmatter parser ───

const FRONTMATTER_RE = /^---\n([\s\S]*?)\n---\n([\s\S]*)$/;

function parseYamlSimple(raw: string): Record<string, unknown> {
	const result: Record<string, unknown> = {};
	const lines = raw.split("\n");
	let currentKey = "";

	for (const line of lines) {
		const trimmed = line.trim();
		if (!trimmed || trimmed.startsWith("#")) continue;

		// Array item: - value or   - value
		if ((trimmed.startsWith("- ") || trimmed.startsWith("  - ")) && currentKey) {
			const existing = result[currentKey];
			const arr = Array.isArray(existing) ? existing : [];
			arr.push(trimmed.replace(/^\s*-\s*/, ""));
			result[currentKey] = arr;
			continue;
		}

		// Key: value
		const colonIdx = trimmed.indexOf(":");
		if (colonIdx === -1) continue;

		const key = trimmed.slice(0, colonIdx).trim();
		const value = trimmed.slice(colonIdx + 1).trim();
		currentKey = key;

		if (value === "true") result[key] = true;
		else if (value === "false") result[key] = false;
		else if (value === "null") result[key] = null;
		else if (/^\d+$/.test(value)) result[key] = parseInt(value, 10);
		else if (/^\d+\.\d+$/.test(value)) result[key] = parseFloat(value);
		else if (value.startsWith('"') && value.endsWith('"')) result[key] = value.slice(1, -1);
		else result[key] = value;
	}

	return result;
}

function dumpYamlSimple(obj: Record<string, unknown>): string {
	const lines: string[] = [];
	for (const [key, val] of Object.entries(obj)) {
		if (Array.isArray(val)) {
			lines.push(`${key}:`);
			for (const item of val) {
				lines.push(`  - ${item}`);
			}
		} else if (val === null || val === undefined) {
			lines.push(`${key}: null`);
		} else if (typeof val === "string") {
			lines.push(`${key}: "${val}"`);
		} else if (typeof val === "number") {
			lines.push(`${key}: ${val}`);
		} else if (typeof val === "boolean") {
			lines.push(`${key}: ${val}`);
		}
	}
	return lines.join("\n");
}

// ─── File paths ───

function memoryFilePath(id: string, scope: MemoryScope): string {
	const memDir = getMemoryDir();
	return join(memDir, scope, `${id}.md`);
}

function parseId(filename: string): string {
	return filename.replace(/\.md$/, "");
}

// ─── CRUD ───

export class MemoryStore {
	private memoryDir: string;

	constructor() {
		this.memoryDir = getMemoryDir();
		mkdirSync(join(this.memoryDir, "local"), { recursive: true });
		mkdirSync(join(this.memoryDir, "cloud"), { recursive: true });
	}

	/** Parse a .md file into a MemoryEntry. Returns null on parse failure. */
	parseFile(filePath: string): MemoryEntry | null {
		try {
			const raw = readFileSync(filePath, "utf-8");
			const match = raw.match(FRONTMATTER_RE);
			if (!match) return null;

			const meta = parseYamlSimple(match[1]) as Record<string, unknown>;
			const content = match[2].trim();

			const entry: MemoryEntry = {
				id: String(meta.id ?? ""),
				scope: (meta.scope as MemoryScope) ?? "cloud",
				type: (meta.type as MemoryType) ?? "episodic",
				priority: (meta.priority as MemoryPriority) ?? "medium",
				tags: Array.isArray(meta.tags) ? meta.tags as string[] : [],
				created: String(meta.created ?? ""),
				updated: String(meta.updated ?? ""),
				call_count: Number(meta.call_count) || 0,
				last_called: meta.last_called ? String(meta.last_called) : null,
				weight: Number(meta.weight) || 0,
				content,
			};

			// Recompute weight on read (handles decay over time)
			entry.weight = calculateWeight(entry);
			return entry;
		} catch {
			return null;
		}
	}

	/** Serialize a MemoryEntry to a .md string. */
	serialize(entry: MemoryEntry): string {
		const frontmatter = dumpYamlSimple({
			id: entry.id,
			scope: entry.scope,
			type: entry.type,
			priority: entry.priority,
			tags: entry.tags,
			created: entry.created,
			updated: entry.updated,
			call_count: entry.call_count,
			last_called: entry.last_called ?? null as unknown as string,
			weight: entry.weight,
		});
		return `---\n${frontmatter}\n---\n\n${entry.content}\n`;
	}

	/** Create a new memory. Returns the entry, or null if already exists. */
	create(entry: Omit<MemoryEntry, "weight">): MemoryEntry | null {
		const filePath = memoryFilePath(entry.id, entry.scope);
		if (existsSync(filePath)) return null;

		const now = new Date().toISOString().slice(0, 10);
		const full: MemoryEntry = {
			...entry,
			created: entry.created || now,
			updated: entry.updated || now,
			call_count: entry.call_count ?? 0,
			weight: calculateWeight(entry as unknown as MemoryMeta),
		};

		mkdirSync(join(this.memoryDir, entry.scope), { recursive: true });
		writeFileSync(filePath, this.serialize(full));
		return full;
	}

	/** Read a memory by id and scope. Returns null if not found. */
	read(id: string, scope: MemoryScope): MemoryEntry | null {
		const filePath = memoryFilePath(id, scope);
		if (!existsSync(filePath)) return null;
		return this.parseFile(filePath);
	}

	/** List all memories, optionally filtered by scope and type. */
	list(filter?: { scope?: MemoryScope; type?: MemoryType; priority?: MemoryPriority }): MemoryEntry[] {
		const entries: MemoryEntry[] = [];
		const memDir = this.memoryDir;

		if (filter?.scope) {
			entries.push(...this.readDir(join(memDir, filter.scope)));
		} else {
			entries.push(...this.readDir(join(memDir, "local")));
			entries.push(...this.readDir(join(memDir, "cloud")));
		}

		// Recompute weights (accounts for time decay)
		for (const entry of entries) {
			entry.weight = calculateWeight(entry);
		}

		if (filter?.type) {
			return entries.filter((e) => e.type === filter!.type);
		}
		if (filter?.priority) {
			return entries.filter((e) => e.priority === filter!.priority);
		}
		return entries;
	}

	/** Update memory metadata and/or content. */
	update(id: string, scope: MemoryScope, patch: Partial<Omit<MemoryEntry, "id" | "scope" | "weight">>): MemoryEntry | null {
		const existing = this.read(id, scope);
		if (!existing) return null;

		const updated: MemoryEntry = {
			...existing,
			...patch,
			id: existing.id,
			scope: existing.scope,
			updated: new Date().toISOString().slice(0, 10),
			weight: 0, // will be recalculated
		};
		updated.weight = calculateWeight(updated);

		const filePath = memoryFilePath(id, scope);
		writeFileSync(filePath, this.serialize(updated));
		return updated;
	}

	/** Record that a memory was used (increments call_count, updates last_called). */
	recordUse(id: string, scope: MemoryScope): void {
		const existing = this.read(id, scope);
		if (!existing) return;

		existing.call_count++;
		existing.last_called = new Date().toISOString();
		existing.weight = calculateWeight(existing);

		const filePath = memoryFilePath(id, scope);
		writeFileSync(filePath, this.serialize(existing));
	}

	/** Delete a memory by id and scope. Returns true if deleted. */
	delete(id: string, scope: MemoryScope): boolean {
		const filePath = memoryFilePath(id, scope);
		if (!existsSync(filePath)) return false;
		unlinkSync(filePath);
		return true;
	}

	private readDir(dir: string): MemoryEntry[] {
		if (!existsSync(dir)) return [];
		const entries: MemoryEntry[] = [];
		for (const filename of readdirSync(dir)) {
			if (extname(filename) !== ".md") continue;
			const entry = this.parseFile(join(dir, filename));
			if (entry) entries.push(entry);
		}
		return entries;
	}
}
