/**
 * Model resolution for half-pi.
 *
 * Combines pi-ai's built-in model registry with custom providers from config.
 * Provides listing, resolution, and interactive switching.
 */

import { getModel, getModels, getProviders } from "@earendil-works/pi-ai";
import type { Model } from "@earendil-works/pi-ai";
import type { CustomProvider, HalfPiConfig, ModelConfig } from "../config.js";

// ─── Types ───

export interface ModelEntry {
	/** Provider name (built-in or custom) */
	provider: string;
	/** Model ID */
	model: string;
	/** Human-readable name */
	name: string;
	/** API type */
	api: string;
	/** Base URL */
	baseUrl: string;
	/** Whether this is a built-in or custom model */
	source: "builtin" | "custom";
	/** Context window size */
	contextWindow: number;
}

// ─── Built-in models ───

/**
 * Get all built-in models from pi-ai's registry.
 */
export function listBuiltinModels(): ModelEntry[] {
	const entries: ModelEntry[] = [];
	for (const provider of getProviders()) {
		try {
			for (const model of getModels(provider)) {
				entries.push({
					provider: model.provider,
					model: model.id,
					name: model.name,
					api: model.api,
					baseUrl: model.baseUrl,
					source: "builtin",
					contextWindow: model.contextWindow,
				});
			}
		} catch {
			// Skip providers whose models can't be listed
		}
	}
	return entries;
}

// ─── Custom providers ───

/**
 * Get custom model entries from config (providers section).
 */
export function listCustomModels(config: HalfPiConfig): ModelEntry[] {
	const entries: ModelEntry[] = [];
	for (const [name, p] of Object.entries(config.providers)) {
		if (!p.base_url) continue;
		const modelIds = p.models ? p.models.split(",").map((s) => s.trim()).filter(Boolean) : ["default"];
		for (const modelId of modelIds) {
			entries.push({
				provider: name,
				model: modelId,
				name: `${name}/${modelId}`,
				api: p.api || "openai-completions",
				baseUrl: p.base_url,
				source: "custom",
				contextWindow: 128000, // default for local models
			});
		}
	}
	return entries;
}

// ─── Combined listing ───

/**
 * List all available models (built-in + custom).
 */
export function listAllModels(config: HalfPiConfig): ModelEntry[] {
	return [...listBuiltinModels(), ...listCustomModels(config)];
}

// ─── Resolution ───

/**
 * Resolve a model from config or CLI args.
 * Uses the first non-null: CLI flags > config default > fallback.
 */
export function resolveModel(
	provider?: string,
	modelId?: string,
	config?: HalfPiConfig,
): Model<any> {
	const effectiveProvider = provider || config?.model.provider || "deepseek";
	const effectiveModel = modelId || config?.model.model || "deepseek-v4-pro";

	// Check if it's a custom provider
	if (config?.providers[effectiveProvider]) {
		return createCustomModel(effectiveProvider, effectiveModel, config.providers[effectiveProvider]);
	}

	// Built-in provider
	try {
		return getModel(effectiveProvider as any, effectiveModel as any) as Model<any>;
	} catch {
		throw new Error(`Unknown model: ${effectiveProvider}/${effectiveModel}`);
	}
}

/**
 * Create a Model object for a custom provider.
 */
function createCustomModel(provider: string, modelId: string, cp: CustomProvider): Model<any> {
	return {
		id: modelId,
		name: modelId,
		api: cp.api || "openai-completions",
		provider,
		baseUrl: cp.base_url,
		reasoning: false,
		input: ["text"],
		cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		contextWindow: 128000,
		maxTokens: 4096,
	} as Model<any>;
}

// ─── Interactive picker ───

import { createInterface } from "node:readline";

/**
 * Interactive model picker. Lists models by provider, lets user pick one.
 * Returns the chosen ModelEntry or null if cancelled.
 */
export async function pickModel(config: HalfPiConfig): Promise<ModelEntry | null> {
	const builtins = listBuiltinModels();
	const customs = listCustomModels(config);

	// Group by provider
	const groups = new Map<string, ModelEntry[]>();
	for (const m of builtins) {
		const list = groups.get(m.provider) || [];
		list.push(m);
		groups.set(m.provider, list);
	}
	for (const m of customs) {
		const list = groups.get(m.provider) || [];
		list.push(m);
		groups.set(m.provider, list);
	}

	// Flatten with provider headers
	const rl = createInterface({ input: process.stdin, output: process.stderr });
	const items: { label: string; entry?: ModelEntry }[] = [];

	for (const [provider, models] of groups) {
		items.push({ label: `\x1b[1m${provider}\x1b[0m (${models.length} models)` });
		for (const m of models.slice(0, 10)) {
			items.push({
				label: `  ${m.model.padEnd(35)} ${m.name}`,
				entry: m,
			});
		}
		if (models.length > 10) {
			items.push({ label: `  ... and ${models.length - 10} more` });
		}
	}

	// Show list
	for (let i = 0; i < items.length; i++) {
		const idx = items[i].entry ? String(i + 1).padStart(4) : "    ";
		process.stderr.write(`${idx}. ${items[i].label}\n`);
	}

	const chosen = await new Promise<ModelEntry | null>((resolve) => {
		rl.question("\nPick a model number (or Enter to cancel): ", (answer) => {
			rl.close();
			const num = parseInt(answer.trim(), 10);
			if (isNaN(num) || num < 1 || num > items.length) {
				resolve(null);
				return;
			}
			resolve(items[num - 1].entry ?? null);
		});
	});

	return chosen;
}
