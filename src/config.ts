/**
 * Paths and configuration for half-pi.
 *
 * Config format: JSONC (JSON with // comments).
 * Path: ~/.half-pi/config.jsonc
 */

import { homedir } from "node:os";
import { join } from "node:path";
import { existsSync, readFileSync } from "node:fs";

/** half-pi data directory: ~/.half-pi/ */
export function getHalfPiDir(): string {
	return join(homedir(), ".half-pi");
}

/** SOUL.md path (legacy, single-file) */
export function getSoulPath(): string {
	return join(getHalfPiDir(), "SOUL.md");
}

/** Core SOUL.md path (new layered design) */
export function getCoreSoulPath(): string {
	return join(getHalfPiDir(), "core.SOUL.md");
}

/** Souls directory (new: souls/<name>/identity.md) */
export function getSoulsDir(): string {
	return join(getHalfPiDir(), "souls");
}

/** Get identity path for a named soul */
export function getSoulIdentityPath(name: string): string {
	return join(getSoulsDir(), name, "identity.md");
}

/** Groups directory (new: groups/<name>.yaml) */
export function getGroupsDir(): string {
	return join(getHalfPiDir(), "groups");
}

/** Get group config path */
export function getGroupConfigPath(name: string): string {
	return join(getGroupsDir(), `${name}.json`);
}

/** Global style/personality file (optional, loaded if exists) */
export function getStylePath(): string {
	return join(getHalfPiDir(), "style.md");
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

/** Config file path */
export function getConfigPath(): string {
	return join(getHalfPiDir(), "config.jsonc");
}

// ─── Config types ───

export interface ModelConfig {
	provider: string;
	model: string;
}

export interface CustomProvider {
	base_url: string;
	api: string; // e.g. "openai-completions", "anthropic-messages"
	/** Optional comma-separated model list (if not discoverable via API) */
	models?: string;
}

export interface HalfPiConfig {
	/** Default model */
	model: ModelConfig;
	/** Custom providers (local models, proxies) */
	providers: Record<string, CustomProvider>;
	/** API keys by provider */
	api_keys: Record<string, string>;
	/** Sync module config */
	sync?: {
		mode: "center" | "leaf" | "off";
		remote?: string;
		port?: number;
		auto_sync_interval?: number;
	};
	/** Sandbox config */
	sandbox?: {
		level: "path_whitelist" | "docker" | "none";
		allowed_paths?: string[];
	};
	/** Web module config */
	web?: {
		enabled: boolean;
		port: number;
		bind: string;
		token?: string;
	};
}

const DEFAULT_CONFIG: HalfPiConfig = {
	model: { provider: "deepseek", model: "deepseek-v4-pro" },
	providers: {},
	api_keys: {},
};

// ─── JSONC parser ───
// Strips // line comments and /* */ block comments, then JSON.parse.

function parseJsonc(raw: string): unknown {
	// Remove block comments first (they can span lines)
	let cleaned = raw.replace(/\/\*[\s\S]*?\*\//g, "");
	// Remove line comments (// to end of line, but not inside strings)
	cleaned = cleaned.replace(/\/\/.*$/gm, "");
	// Remove trailing commas (JSON5-ish tolerance)
	cleaned = cleaned.replace(/,(\s*[}\]])/g, "$1");
	return JSON.parse(cleaned);
}

/**
 * Load half-pi configuration from ~/.half-pi/config.jsonc.
 * Falls back to defaults if the file doesn't exist or is malformed.
 */
export function loadConfig(): HalfPiConfig {
	const configPath = getConfigPath();
	if (!existsSync(configPath)) return { ...DEFAULT_CONFIG };

	try {
		const raw = readFileSync(configPath, "utf-8");
		const parsed = parseJsonc(raw) as Record<string, unknown>;

		return {
			model: parseModel(parsed["model"]) ?? DEFAULT_CONFIG.model,
			providers: parseProviders(parsed["providers"]) ?? {},
			api_keys: parseStringMap(parsed["api_keys"]) ?? {},
			sync: parseSync(parsed["sync"]),
			sandbox: parseSandbox(parsed["sandbox"]),
			web: parseWeb(parsed["web"]),
		};
	} catch {
		return { ...DEFAULT_CONFIG };
	}
}

function parseModel(raw: unknown): ModelConfig | null {
	if (!raw || typeof raw !== "object") return null;
	const m = raw as Record<string, unknown>;
	const provider = typeof m.provider === "string" ? m.provider : "";
	const model = typeof m.model === "string" ? m.model : "";
	return provider && model ? { provider, model } : null;
}

function parseProviders(raw: unknown): Record<string, CustomProvider> {
	if (!raw || typeof raw !== "object") return {};
	const result: Record<string, CustomProvider> = {};
	for (const [name, val] of Object.entries(raw as Record<string, unknown>)) {
		if (val && typeof val === "object") {
			const p = val as Record<string, unknown>;
			result[name] = {
				base_url: typeof p.base_url === "string" ? p.base_url : "",
				api: typeof p.api === "string" ? p.api : "openai-completions",
				models: typeof p.models === "string" ? p.models : undefined,
			};
		}
	}
	return result;
}

function parseStringMap(raw: unknown): Record<string, string> {
	if (!raw || typeof raw !== "object") return {};
	const result: Record<string, string> = {};
	for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
		if (typeof v === "string") result[k] = v;
	}
	return result;
}

function parseSync(raw: unknown): HalfPiConfig["sync"] {
	if (!raw || typeof raw !== "object") return undefined;
	const s = raw as Record<string, unknown>;
	return {
		mode: (s.mode === "center" || s.mode === "leaf" || s.mode === "off" ? s.mode : "off") as "center" | "leaf" | "off",
		remote: typeof s.remote === "string" ? s.remote : undefined,
		port: typeof s.port === "number" ? s.port : undefined,
		auto_sync_interval: typeof s.auto_sync_interval === "number" ? s.auto_sync_interval : undefined,
	};
}

function parseSandbox(raw: unknown): HalfPiConfig["sandbox"] {
	if (!raw || typeof raw !== "object") return undefined;
	const sb = raw as Record<string, unknown>;
	return {
		level: (sb.level === "path_whitelist" || sb.level === "docker" || sb.level === "none" ? sb.level : "none") as "path_whitelist" | "docker" | "none",
		allowed_paths: Array.isArray(sb.allowed_paths)
			? sb.allowed_paths.filter((p): p is string => typeof p === "string")
			: undefined,
	};
}

function parseWeb(raw: unknown): HalfPiConfig["web"] {
	if (!raw || typeof raw !== "object") return undefined;
	const w = raw as Record<string, unknown>;
	return {
		enabled: w.enabled === true,
		port: typeof w.port === "number" ? w.port : 3000,
		bind: typeof w.bind === "string" ? w.bind : "127.0.0.1",
		token: typeof w.token === "string" ? w.token : undefined,
	};
}

/**
 * Set API keys from config into process.env so pi-ai can find them.
 */
export function applyApiKeys(config: HalfPiConfig): void {
	for (const [provider, key] of Object.entries(config.api_keys)) {
		if (key && !process.env[`${provider.toUpperCase()}_API_KEY`]) {
			const envName = providerEnvVar(provider);
			if (envName) process.env[envName] = key;
		}
	}
}

/** Map provider name to the env var pi-ai expects. */
function providerEnvVar(provider: string): string | null {
	const map: Record<string, string> = {
		deepseek: "DEEPSEEK_API_KEY",
		openai: "OPENAI_API_KEY",
		anthropic: "ANTHROPIC_API_KEY",
		google: "GEMINI_API_KEY",
		groq: "GROQ_API_KEY",
		cerebras: "CEREBRAS_API_KEY",
		xai: "XAI_API_KEY",
		openrouter: "OPENROUTER_API_KEY",
		mistral: "MISTRAL_API_KEY",
		minimax: "MINIMAX_API_KEY",
		moonshotai: "MOONSHOT_API_KEY",
		huggingface: "HF_API_KEY",
		together: "TOGETHER_API_KEY",
		fireworks: "FIREWORKS_API_KEY",
	};
	return map[provider] || `${provider.toUpperCase()}_API_KEY`;
}
