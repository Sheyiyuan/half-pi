#!/usr/bin/env node
/**
 * half-pi CLI — entry point.
 *
 * Usage:
 *   half-pi "prompt"                  Run a single prompt
 *   half-pi --soul                    View current SOUL.md
 *   half-pi models                    List available models
 *   half-pi models switch             Interactive model picker
 *   half-pi models current            Show current default model
 */
import { resolve } from "node:path";
import { createInterface } from "node:readline";
import { getModel } from "@earendil-works/pi-ai";
import type { Model } from "@earendil-works/pi-ai";
import type { AgentEvent } from "@earendil-works/pi-agent-core";
import { AgentSession } from "./core/agent-session.js";
import { loadSoul } from "./core/soul-loader.js";
import { applyApiKeys, loadConfig } from "./config.js";
import type { HalfPiConfig } from "./config.js";
import { DEFAULT_TOOLS } from "./core/tools.js";
import {
	listAllModels,
	pickModel,
	resolveModel,
	type ModelEntry,
} from "./core/model-resolver.js";

// ─── Event display ───

let currentSoulLabel: string = "";

function setSoulLabel(name: string): void {
	currentSoulLabel = name;
}

function formatEvent(event: AgentEvent, suppressRaw: boolean = false): void {
	switch (event.type) {
		case "message_update": {
			const ame = event.assistantMessageEvent;
			if (ame.type === "text_delta" && !suppressRaw) {
				process.stdout.write(ame.delta);
			}
			break;
		}
		case "tool_execution_start":
			// Suppress speak tool display (text is handled by onSpeakDisplay)
			if (event.toolName !== "speak") {
				process.stderr.write(`\n  ⚙ ${event.toolName}... `);
			}
			break;
		case "tool_execution_end":
			if (event.toolName !== "speak") {
				process.stderr.write("done\n");
			}
			break;
		case "turn_end":
		case "agent_start":
		case "agent_end":
			break;
	}
}

// ─── Help ───

function showHelp(): void {
	console.log("half-pi v0.1.0 — half cloud, half local");
	console.log("");
	console.log("Usage:");
	console.log("  half-pi 'prompt'              Run a single prompt");
	console.log("  half-pi chat                  Start interactive chat session");
	console.log("  half-pi --soul                 View current SOUL.md");
	console.log("  half-pi --model <id>           Override model for this run");
	console.log("  half-pi --provider <name>      Override provider for this run");
	console.log("  half-pi --group <name>         Use group config (default: daily)");
	console.log("  half-pi models                 List available models");
	console.log("  half-pi models switch          Interactive model picker");
	console.log("  half-pi models current         Show current default");
	console.log("");
	console.log("Config:");
	console.log("  ~/.half-pi/config.jsonc     — API keys, default model, modules");
	console.log("  ~/.half-pi/core.SOUL.md     — Core commitment (all souls)");
	console.log("  ~/.half-pi/souls/<name>/    — Soul identity + memory");
	console.log("  ~/.half-pi/groups/<name>.yaml — Group config");
	console.log("  ~/.half-pi/skills/          — Reusable skill documents");
}

// ─── Model listing ───

function showModels(config: HalfPiConfig): void {
	const builtins = listAllModels(config);
	const shown = new Set<string>();
	let count = 0;

	for (const m of builtins) {
		const key = `${m.provider}/${m.model}`;
		if (shown.has(key)) continue;
		shown.add(key);

		const tag = m.source === "custom" ? "[custom]" : "";
		const ctx = m.contextWindow >= 1000000
			? `${(m.contextWindow / 1000000).toFixed(0)}M`
			: `${(m.contextWindow / 1000).toFixed(0)}K`;
		console.log(`  ${m.provider.padEnd(22)} ${m.model.padEnd(32)} ${ctx.padEnd(5)} ${tag} ${m.name}`);
		count++;
	}
	console.log(`\n${count} models total (pi-ai built-in + custom providers).`);
}

function showCurrent(config: HalfPiConfig): void {
	const m = config.model;
	console.log(`Current default: ${m.provider}/${m.model}`);
}

// ─── Dangerous command detection ───

type DangerLevel = "critical" | "warning" | "safe";

interface DangerRule {
	pattern: RegExp;
	level: DangerLevel;
	label: string;
}

const DANGER_RULES: DangerRule[] = [
	{ pattern: /\brm\s+(-[a-zA-Z]*[rRf]|-[a-zA-Z]*[Rr])\b.*\/(dev|etc|proc|sys|boot|home\/(?!.*workspace).*)/, level: "critical", label: "rm -rf on system paths" },
	{ pattern: /\bsudo\b/, level: "critical", label: "sudo (privilege escalation)" },
	{ pattern: /\bmkfs\b/, level: "critical", label: "mkfs (format filesystem)" },
	{ pattern: /\bdd\s+if=/, level: "critical", label: "dd (raw disk write)" },
	{ pattern: /\bchmod\s+.*777\b/, level: "critical", label: "chmod 777" },
	{ pattern: /\bchmod\s+(-R|--recursive)\b/, level: "warning", label: "chmod recursive" },
	{ pattern: /\bchown\b/, level: "warning", label: "chown (change ownership)" },
	{ pattern: />\s*\/dev\/(sd|nvme|hd)/, level: "critical", label: "write to block device" },
	{ pattern: /\brm\s+(-[a-zA-Z]*[rRf]|-[a-zA-Z]*[Rr])\b/, level: "warning", label: "rm -rf" },
	{ pattern: /\bgit\s+push\s+.*--force\b/, level: "warning", label: "git push --force" },
];

function checkDanger(command: string): { level: DangerLevel; label: string } | null {
	for (const rule of DANGER_RULES) {
		if (rule.pattern.test(command)) {
			return { level: rule.level, label: rule.label };
		}
	}
	return null;
}

// ─── Chat REPL ───

async function confirmBash(_toolName: string, params: Record<string, unknown>): Promise<boolean> {
	const command = typeof params.command === "string" ? params.command : "";
	const danger = checkDanger(command);
	if (!danger) return true;

	const prefix = danger.level === "critical" ? "\x1b[1;31mDANGER\x1b[0m" : "\x1b[1;33mWARNING\x1b[0m";
	process.stderr.write(`\n  ${prefix}: ${danger.label}\n`);
	process.stderr.write(`  command: \x1b[90m${command}\x1b[0m\n`);

	const rl = createInterface({ input: process.stdin, output: process.stderr });
	const answer = await new Promise<string>((resolve) => {
		rl.question(`  Execute? [y/N] `, (a) => { rl.close(); resolve(a.trim().toLowerCase()); });
	});

	return answer === "y" || answer === "yes";
}

async function autoRejectBash(_toolName: string, params: Record<string, unknown>): Promise<boolean> {
	const command = typeof params.command === "string" ? params.command : "";
	const danger = checkDanger(command);
	if (!danger) return true;

	process.stderr.write(`\n  \x1b[1;31mBLOCKED\x1b[0m: ${danger.label}\n`);
	process.stderr.write(`  command: \x1b[90m${command}\x1b[0m\n`);
	process.stderr.write(`  Run in chat mode (half-pi chat) to confirm interactively.\n`);
	return false;
}

	async function runChat(model: Model<any>, config: HalfPiConfig, cwd: string, systemPrompt?: string, groupName?: string): Promise<void> {
	// Flag to suppress raw LLM text output (in director mode, only speak tool emits text)
	let suppressRawText = false;

	const session = new AgentSession({
		cwd,
		model,
		customSystemPrompt: systemPrompt,
		onEvent: (event) => formatEvent(event, suppressRawText),
		confirmDangerous: confirmBash,
		groupName,
		onSoulSwitch: setSoulLabel,
		onSpeakDisplay: (soul, text) => {
			process.stdout.write(`[${soul}] ${text}\n`);
		},
	});

	suppressRawText = groupName ? true : false;

	// Initialize soul label
	setSoulLabel(session.currentSoul);

	console.log(`[half-pi] ${model.provider}/${model.id}  cwd: ${cwd}`);
	const soulInfo = loadSoul();
	const soulLabel = soulInfo.source.includes("builtin") ? "built-in" : soulInfo.source;
	console.log(`[half-pi] SOUL: ${soulLabel}`);
	if (groupName) {
		console.log(`[half-pi] GROUP: ${groupName}  [${session.groupSouls.join(", ")}]`);
	}
	console.log(`[half-pi] /help for commands, /exit to quit\n`);

	const rl = createInterface({ input: process.stdin, output: process.stderr });
	const prompt = () => { rl.setPrompt("\x1b[1m>\x1b[0m "); rl.prompt(); };
	prompt();

	let running = false;
	let aborted = false;

	const abort = () => {
		if (running) {
			session.abort();
			aborted = true;
			process.stderr.write("\n[aborted]\n");
		}
	};

	process.on("SIGINT", abort);

	for await (const line of rl) {
		const input = line.trim();

		if (aborted) {
			aborted = false;
			prompt();
			continue;
		}

		if (!input) {
			prompt();
			continue;
		}

		if (input === "/exit" || input === "/quit") {
			console.log("bye");
			rl.close();
			process.exit(0);
		}

		if (input === "/help") {
			console.log("  /exit, /quit    Quit");
			console.log("  /models         List available models");
			console.log("  Ctrl+C          Abort current prompt");
			console.log("");
			prompt();
			continue;
		}

		if (input === "/models") {
			showModels(config);
			prompt();
			continue;
		}

		running = true;
		try {
			await session.prompt(input);
			process.stdout.write("\n");
		} catch (err) {
			process.stderr.write(`\n[error] ${err instanceof Error ? err.message : String(err)}\n`);
		} finally {
			running = false;
		}

		prompt();
	}
}

// ─── Main ───

async function main(): Promise<void> {
	const args = process.argv.slice(2);

	// Subcommands
	const subcmd = args[0];

	// chat — interactive REPL
	if (subcmd === "chat") {
		// Parse --group from args
		let chatGroup: string | undefined;
		for (let i = 1; i < args.length; i++) {
			if (args[i] === "--group" || args[i] === "-g") {
				chatGroup = args[i + 1];
				break;
			}
		}

		const config = loadConfig();
		applyApiKeys(config);
		const model = resolveModel(undefined, undefined, config);
		const apiKeyEnv = model.provider.toUpperCase() + "_API_KEY";
		if (!process.env[apiKeyEnv] && !config.providers[model.provider]) {
			console.error("[half-pi] No API key. Set in ~/.half-pi/config.jsonc:");
			console.error('  "api_keys": { "' + model.provider + '": "sk-your-key" }');
			process.exit(1);
		}
		await runChat(model, config, process.cwd(), undefined, chatGroup);
		process.exit(0);
	}

	if (subcmd === "models") {
		const config = loadConfig();
		const action = args[1];

		if (action === "switch") {
			const chosen = await pickModel(config);
			if (chosen) {
				console.log(`\nSelected: ${chosen.provider}/${chosen.model} (${chosen.name})`);
				console.log(`To set as default, update ~/.half-pi/config.jsonc:`);
				console.log(`  "model": { "provider": "${chosen.provider}", "model": "${chosen.model}" }`);
			}
			process.exit(0);
		}

		if (action === "current") {
			showCurrent(config);
			process.exit(0);
		}

		// List all models
		showModels(config);
		process.exit(0);
	}

	// --soul flag
	if (args[0] === "--soul" || args[0] === "-s") {
		const soul = loadSoul();
		console.log(soul.identity);
		console.log(`\n[source: ${soul.source}]`);
		process.exit(0);
	}

	// Parse flags + prompt
	let prompt: string | undefined;
	let modelId: string | undefined;
	let provider: string | undefined;
	let systemPrompt: string | undefined;
	let groupName: string | undefined;
	let cwd = process.cwd();

	for (let i = 0; i < args.length; i++) {
		const arg = args[i];
		if (arg === "--model" || arg === "-m") {
			modelId = args[++i];
		} else if (arg === "--provider" || arg === "-P") {
			provider = args[++i];
		} else if (arg === "--system-prompt" || arg === "-p") {
			systemPrompt = args[++i];
		} else if (arg === "--cwd") {
			cwd = resolve(args[++i]);
		} else if (arg === "--group" || arg === "-g") {
			groupName = args[++i];
		} else if (!arg.startsWith("-") && arg !== "models" && arg !== "chat") {
			prompt = arg;
		}
	}

	if (!prompt) {
		showHelp();
		process.exit(0);
	}

	// Load config and set API keys
	const config = loadConfig();
	applyApiKeys(config);

	// Resolve model
	const effectiveProvider = provider || config.model.provider;
	const effectiveModel = modelId || config.model.model;
	console.log(`[half-pi] ${effectiveProvider}/${effectiveModel}  cwd: ${cwd}`);

	// Check API key
	const apiKeyEnv = `${effectiveProvider.toUpperCase()}_API_KEY`;
	if (!process.env[apiKeyEnv] && !config.providers[effectiveProvider]) {
		console.error(`[half-pi] No API key. Set in ~/.half-pi/config.jsonc:`);
		console.error(`  "api_keys": { "${effectiveProvider}": "sk-your-key" }`);
		process.exit(1);
	}

	let model: Model<any>;
	try {
		model = resolveModel(provider, modelId, config);
	} catch (err) {
		console.error(`[half-pi] ${err instanceof Error ? err.message : String(err)}`);
		process.exit(1);
	}

	// Create session
	const session = new AgentSession({
		cwd,
		model,
		customSystemPrompt: systemPrompt,
		onEvent: formatEvent,
		confirmDangerous: autoRejectBash,
	});

	const soulInfo = loadSoul();
	const soulLabel = soulInfo.source.includes("builtin") ? "built-in" : soulInfo.source;
	console.log(`[half-pi] SOUL: ${soulLabel}`);
	console.log(`[half-pi] Tools: ${DEFAULT_TOOLS.length} registered\n`);

	try {
		await session.prompt(prompt);
	} catch (err) {
		console.error(`\n[half-pi] Error: ${err instanceof Error ? err.message : String(err)}`);
		process.exit(1);
	}
}

main().catch((err) => {
	console.error("half-pi:", err);
	process.exit(1);
});
