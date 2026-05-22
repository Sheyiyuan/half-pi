/**
 * Session storage — persistence layer for half-pi conversations.
 *
 * Lifecycle: hot → warm → cold → deleted (forgetting curve)
 *   hot/   — full messages, active sessions
 *   warm/  — compressed, inactive > hot_days
 *   cold   — summary only in index.json, file deleted
 *
 * Locking: .locks/{id}.lock prevents concurrent access.
 * Index: atomic write (tmp + rename) to avoid corruption.
 */

import { randomBytes } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync, unlinkSync } from "node:fs";
import { join, dirname } from "node:path";
import { getSessionsDir } from "../config.js";

// ─── Types ───

export type SessionPhase = "hot" | "warm" | "cold";

export interface SessionIndexEntry {
	phase: SessionPhase;
	path: string | null; // relative to sessions dir, null if cold
	title: string;
	created: string;   // ISO timestamp
	last_active: string;
	msg_count: number;
	warm_msg_count?: number;
	degraded_at: string | null;
	summary?: string;
	parent?: string;   // parent session ID (for forks)
}

export interface SessionIndex {
	version: number;
	sessions: Record<string, SessionIndexEntry>;
}

export interface SessionData {
	id: string;
	messages: unknown[];
	phase: SessionPhase;
	entry: SessionIndexEntry;
}

export interface SessionStoreOptions {
	/** Days until hot → warm (default: 7) */
	hotDays?: number;
	/** Days until warm → cold (default: 30) */
	warmDays?: number;
	/** Days until cold → deleted (default: 90) */
	coldDays?: number;
}

const DEFAULT_OPTIONS: Required<SessionStoreOptions> = {
	hotDays: 7,
	warmDays: 30,
	coldDays: 90,
};

// ─── SessionStore ───

export class SessionStore {
	private sessionsDir: string;
	private hotDir: string;
	private warmDir: string;
	private locksDir: string;
	private indexPath: string;
	private options: Required<SessionStoreOptions>;

	constructor(options: SessionStoreOptions = {}) {
		this.options = { ...DEFAULT_OPTIONS, ...options };
		this.sessionsDir = getSessionsDir();
		this.hotDir = join(this.sessionsDir, "hot");
		this.warmDir = join(this.sessionsDir, "warm");
		this.locksDir = join(this.sessionsDir, ".locks");
		this.indexPath = join(this.sessionsDir, "index.json");
	}

	// ─── ID generation ───

	/** Generate a unique session ID: {YYYYMMDD}-{6-char shortId} */
	generateId(): string {
		const now = new Date();
		const date = [
			now.getFullYear(),
			String(now.getMonth() + 1).padStart(2, "0"),
			String(now.getDate()).padStart(2, "0"),
		].join("");
		const shortId = randomBytes(4)
			.toString("base64url")
			.slice(0, 6);
		return `${date}-${shortId}`;
	}

	// ─── Locking ───

	private lockPath(id: string): string {
		return join(this.locksDir, `${id}.lock`);
	}

	/** Try to acquire lock. Returns true if acquired, false if another process holds it. */
	acquireLock(id: string): boolean {
		mkdirSync(this.locksDir, { recursive: true });
		const path = this.lockPath(id);

		if (existsSync(path)) {
			// Check if the lock holder is still alive
			try {
				const raw = readFileSync(path, "utf-8");
				const lock = JSON.parse(raw) as { pid: number };
				// Signal 0 checks existence without killing
				try { process.kill(lock.pid, 0); } catch {
					// Process is dead — stale lock, remove it
					unlinkSync(path);
				}
			} catch {
				// Corrupt lock file — remove it
				try { unlinkSync(path); } catch { /* fine */ }
			}
		}

		if (existsSync(path)) return false; // still locked

		// Create lock
		const lockData = {
			pid: process.pid,
			started_at: new Date().toISOString(),
			hostname: process.env.HOSTNAME ?? "unknown",
		};
		writeFileSync(path, JSON.stringify(lockData), { flag: "wx" });
		return true;
	}

	/** Release lock. Silent if no lock held. */
	releaseLock(id: string): void {
		const path = this.lockPath(id);
		try { unlinkSync(path); } catch { /* fine */ }
	}

	// ─── Index management ───

	private readIndex(): SessionIndex {
		if (!existsSync(this.indexPath)) {
			return { version: 1, sessions: {} };
		}
		try {
			return JSON.parse(readFileSync(this.indexPath, "utf-8")) as SessionIndex;
		} catch {
			return { version: 1, sessions: {} };
		}
	}

	/** Atomic write: write to temp file, then rename. */
	private writeIndex(index: SessionIndex): void {
		mkdirSync(dirname(this.indexPath), { recursive: true });
		const tmp = this.indexPath + ".tmp";
		writeFileSync(tmp, JSON.stringify(index, null, 2));
		renameSync(tmp, this.indexPath);
	}

	private updateIndexEntry(id: string, entry: SessionIndexEntry): void {
		const index = this.readIndex();
		index.sessions[id] = entry;
		this.writeIndex(index);
	}

	private removeIndexEntry(id: string): void {
		const index = this.readIndex();
		delete index.sessions[id];
		this.writeIndex(index);
	}

	// ─── Create ───

	/**
	 * Create a new session.
	 * Returns the session ID. Saves empty messages and index entry.
	 */
	create(title?: string): string {
		mkdirSync(this.hotDir, { recursive: true });
		const id = this.generateId();
		const now = new Date().toISOString();

		const entry: SessionIndexEntry = {
			phase: "hot",
			path: `hot/${id}.json`,
			title: title ?? "(新会话)",
			created: now,
			last_active: now,
			msg_count: 0,
			degraded_at: null,
		};

		// Save empty messages
		const filePath = join(this.sessionsDir, entry.path!);
		mkdirSync(dirname(filePath), { recursive: true });
		writeFileSync(filePath, JSON.stringify([], null, 2));

		// Update index
		this.updateIndexEntry(id, entry);

		return id;
	}

	// ─── Save ───

	/**
	 * Save messages for a session. Updates last_active and msg_count.
	 * Only works for hot-phase sessions.
	 */
	save(id: string, messages: unknown[], title?: string): void {
		const index = this.readIndex();
		const entry = index.sessions[id];
		if (!entry || entry.phase !== "hot") return;

		const now = new Date().toISOString();

		// Write messages
		const filePath = join(this.sessionsDir, entry.path!);
		mkdirSync(dirname(filePath), { recursive: true });
		writeFileSync(filePath, JSON.stringify(messages, null, 2));

		// Update entry
		entry.last_active = now;
		entry.msg_count = messages.length;
		if (title) entry.title = title;

		index.sessions[id] = entry;
		this.writeIndex(index);
	}

	// ─── Load ───

	/**
	 * Load a session by ID.
	 * Returns SessionData or null if not found / cold.
	 * If warm, the session is promoted back to hot (lossy — warm compression lost details).
	 */
	load(id: string): SessionData | null {
		const index = this.readIndex();
		const entry = index.sessions[id];
		if (!entry) return null;

		// Cold sessions: data is gone
		if (entry.phase === "cold") {
			return {
				id,
				messages: [],
				phase: "cold",
				entry,
			};
		}

		// Hot or warm: read file
		const filePath = join(this.sessionsDir, entry.path!);
		if (!existsSync(filePath)) {
			// File missing — treat as cold
			entry.phase = "cold";
			entry.path = null;
			this.updateIndexEntry(id, entry);
			return { id, messages: [], phase: "cold", entry };
		}

		let messages: unknown[];
		try {
			const raw = readFileSync(filePath, "utf-8");
			messages = JSON.parse(raw) as unknown[];
		} catch {
			return null;
		}

		// Promote warm → hot
		if (entry.phase === "warm") {
			const now = new Date().toISOString();
			const hotPath = `hot/${id}.json`;
			const newFilePath = join(this.sessionsDir, hotPath);

			// Move file from warm to hot
			mkdirSync(dirname(newFilePath), { recursive: true });
			writeFileSync(newFilePath, JSON.stringify(messages, null, 2));
			try { unlinkSync(filePath); } catch { /* fine */ }

			entry.phase = "hot";
			entry.path = hotPath;
			entry.last_active = now;
			// warm_msg_count stays — it records the compression history
			this.updateIndexEntry(id, entry);
		}

		return { id, messages, phase: "hot", entry };
	}

	/**
	 * Load the most recent session (by last_active).
	 * Returns null if no sessions exist or the most recent is cold.
	 */
	loadLast(): SessionData | null {
		const index = this.readIndex();
		let latest: { id: string; entry: SessionIndexEntry } | null = null;

		for (const [id, entry] of Object.entries(index.sessions)) {
			// Skip cold — can't recover
			if (entry.phase === "cold") continue;
			if (!latest || entry.last_active > latest.entry.last_active) {
				latest = { id, entry };
			}
		}

		if (!latest) return null;
		return this.load(latest.id);
	}

	// ─── List ───

	/** List all sessions from index. */
	listSessions(): SessionIndexEntry[] {
		const index = this.readIndex();
		return Object.entries(index.sessions)
			.map(([, entry]) => entry)
			.sort((a, b) => b.last_active.localeCompare(a.last_active));
	}

	// ─── Compression / migration hooks (P3/P4, stubs for now) ───

	/**
	 * Get the absolute path to a session file for a given ID.
	 * Returns null if the session doesn't exist or is cold.
	 */
	getFilePath(id: string): string | null {
		const index = this.readIndex();
		const entry = index.sessions[id];
		if (!entry || !entry.path) return null;
		return join(this.sessionsDir, entry.path);
	}

	/**
	 * Force a session to warm/cold phase (for migration pipeline).
	 * This is a low-level operation — P4 will add proper compression.
	 */
	forcePhase(id: string, phase: SessionPhase, summary?: string): void {
		const index = this.readIndex();
		const entry = index.sessions[id];
		if (!entry) return;

		const now = new Date().toISOString();

		if (phase === "cold") {
			// Delete file
			if (entry.path) {
				const fp = join(this.sessionsDir, entry.path);
				try { unlinkSync(fp); } catch { /* fine */ }
			}
			entry.path = null;
			entry.summary = summary;
		}

		entry.phase = phase;
		entry.degraded_at = now;
		this.updateIndexEntry(id, entry);
	}
}

/**
 * Extract a title from the first user message.
 * Truncated to 60 chars.
 */
export function titleFromFirstMessage(messages: unknown[]): string {
	for (const msg of messages) {
		const m = msg as Record<string, unknown>;
		if (m.role === "user") {
			const content = m.content;
			let text = "";
			if (typeof content === "string") {
				text = content;
			} else if (Array.isArray(content)) {
				for (const block of content as Array<Record<string, unknown>>) {
					if (block.type === "text" && typeof block.text === "string") {
						text += block.text;
					}
				}
			}
			text = text.trim();
			if (text) {
				return text.length > 60 ? text.slice(0, 57) + "..." : text;
			}
		}
	}
	return "(空会话)";
}
