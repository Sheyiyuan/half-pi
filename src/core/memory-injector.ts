/**
 * Memory injector — load, filter, sort, and format memories for system prompt injection.
 *
 * Pipeline:
 *   1. Load all memories from memory store
 *   2. Filter: weight > threshold (default 0.3)
 *   3. Sort: weight descending
 *   4. Trim to token budget
 *   5. Format as <memory> tags
 *
 * Token estimate: ~4 chars per token (conservative heuristic)
 */

import { MemoryStore, type MemoryEntry } from "./memory-store.js";

// ─── Token estimation ───

/** Conservative heuristic: ~4 chars per token for Chinese + English mixed text. */
function estimateTokens(text: string): number {
	return Math.ceil(text.length / 4);
}

// ─── Injector ───

export interface MemoryInjectOptions {
	/** Minimum weight to include (default: 0.3) */
	threshold?: number;
	/** Maximum token budget for memory section (default: contextWindow * 5%) */
	maxTokens?: number;
	/** Context window of the model (default: 200000) */
	contextWindow?: number;
}

const DEFAULT_OPTIONS: Required<MemoryInjectOptions> = {
	threshold: 0.3,
	contextWindow: 200_000,
	get maxTokens(): number {
		return Math.floor(this.contextWindow * 0.05);
	},
};

export class MemoryInjector {
	private store: MemoryStore;

	constructor(store?: MemoryStore) {
		this.store = store ?? new MemoryStore();
	}

	/**
	 * Build the memory section to inject into system prompt.
	 * Returns empty string if no memories pass the filter.
	 */
	buildMemorySection(options: MemoryInjectOptions = {}): string {
		const opts = { ...DEFAULT_OPTIONS, ...options };

		// Override maxTokens if explicitly set, otherwise use percentage
		const budget = options.maxTokens ?? Math.floor(opts.contextWindow * 0.05);
		const threshold = options.threshold ?? DEFAULT_OPTIONS.threshold;

		// 1. Load
		const all = this.store.list();

		// 2. Filter by weight and exclude archive
		const active = all.filter(
			(m) => m.weight > threshold && m.priority !== "archive"
		);

		if (active.length === 0) return "";

		// 3. Sort by weight descending
		active.sort((a, b) => b.weight - a.weight);

		// 4. Trim to token budget
		const selected = this.trimToBudget(active, budget);

		// 5. Format
		const formatted = this.formatMemories(selected);
		const omitted = active.length - selected.length;

		let section = "## Memory\n\n" + formatted;
		if (omitted > 0) {
			section += `\n... (${omitted} more memories omitted, use memory:search to retrieve)`;
		}

		return section;
	}

	/** Trim entries to fit within token budget. Keeps highest-weight entries. */
	private trimToBudget(entries: MemoryEntry[], budget: number): MemoryEntry[] {
		const result: MemoryEntry[] = [];
		let used = 0;
		const sectionOverhead = estimateTokens("## Memory\n\n");

		for (const entry of entries) {
			const tag = this.formatMemory(entry);
			const cost = estimateTokens(tag);
			if (used + cost + sectionOverhead > budget) break;
			result.push(entry);
			used += cost;
		}

		return result;
	}

	/** Format a single memory as an XML tag. */
	private formatMemory(entry: MemoryEntry): string {
		return `<memory id="${entry.id}" weight="${entry.weight.toFixed(2)}">\n${entry.content}\n</memory>\n`;
	}

	/** Format multiple memories. */
	private formatMemories(entries: MemoryEntry[]): string {
		return entries.map((e) => this.formatMemory(e)).join("\n");
	}

	/**
	 * Record that a set of memories was injected (updates call_count and last_called).
	 * Should be called after system prompt is built and sent.
	 */
	recordInjection(memories: MemoryEntry[]): void {
		for (const mem of memories) {
			this.store.recordUse(mem.id, mem.scope);
		}
	}

	/**
	 * Get the list of memories that would be injected (without formatting).
	 * Useful for call tracking.
	 */
	selectMemories(options: MemoryInjectOptions = {}): MemoryEntry[] {
		const all = this.store.list();
		return all
			.filter((m) => m.weight > (options.threshold ?? 0.3) && m.priority !== "archive")
			.sort((a, b) => b.weight - a.weight);
	}
}
