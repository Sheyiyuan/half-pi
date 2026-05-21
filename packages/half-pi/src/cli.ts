#!/usr/bin/env node
/**
 * half-pi CLI — entry point.
 *
 * Usage:
 *   half-pi "prompt"              Run a single prompt (print mode)
 *   half-pi                        Start interactive mode (TBD)
 *   half-pi --soul                 View current SOUL.md
 *   half-pi --system-prompt <text> Override SOUL.md identity
 *   half-pi --model <model>        Specify model
 */
import { resolve } from "node:path";
import { AgentSession } from "./core/agent-session.ts";
import { loadSoul } from "./core/soul-loader.ts";
import { DEFAULT_TOOLS } from "./core/tools.ts";

interface ParsedArgs {
	prompt?: string;
	soulFlag: boolean;
	systemPrompt?: string;
	modelId?: string;
	cwd: string;
}

function parseArgs(args: string[]): ParsedArgs {
	const result: ParsedArgs = {
		soulFlag: false,
		cwd: process.cwd(),
	};

	for (let i = 0; i < args.length; i++) {
		const arg = args[i];
		if (arg === "--soul" || arg === "-s") {
			result.soulFlag = true;
		} else if ((arg === "--system-prompt" || arg === "-p") && i + 1 < args.length) {
			result.systemPrompt = args[++i];
		} else if ((arg === "--model" || arg === "-m") && i + 1 < args.length) {
			result.modelId = args[++i];
		} else if (arg === "--cwd" && i + 1 < args.length) {
			result.cwd = resolve(args[++i]);
		} else if (!arg.startsWith("-")) {
			result.prompt = arg;
		}
	}

	return result;
}

async function main(): Promise<void> {
	const args = parseArgs(process.argv.slice(2));

	// --soul flag: just print SOUL.md and exit
	if (args.soulFlag) {
		const soul = loadSoul();
		console.log(soul.content);
		console.log(`\n[source: ${soul.source}]`);
		process.exit(0);
	}

	// If no prompt given, show help (interactive mode not yet implemented)
	if (!args.prompt) {
		console.log("half-pi v0.1.0 — half cloud, half local");
		console.log("");
		console.log("Usage:");
		console.log("  half-pi 'prompt'                  Run a single prompt");
		console.log("  half-pi --soul                     View current SOUL.md");
		console.log("  half-pi --system-prompt <text>     Override SOUL.md for this run");
		console.log("  half-pi --model <model>            Specify model");
		console.log("  half-pi --cwd <path>              Set working directory");
		console.log("");
		console.log("Config:");
		console.log("  ~/.half-pi/SOUL.md     — Agent identity");
		console.log("  ~/.half-pi/skills/     — Reusable skill documents");
		console.log("  ~/.half-pi/memory/     — Persistent memory (coming soon)");
		console.log("  ~/.half-pi/sessions/   — Session transcripts");
		process.exit(0);
	}

	// Use pi's model auto-detection. For now, hardcode a default.
	// In production this would use pi-ai's model registry.
	console.log(`[half-pi] Starting with prompt: "${args.prompt}"`);
	console.log(`[half-pi] cwd: ${args.cwd}`);
	console.log("");

	// TODO: Use pi-ai's model auto-detection. For skeleton, skip the actual LLM call.
	console.log("[half-pi] Agent core skeleton initialized.");
	console.log("[half-pi] SOUL.md: " + (loadSoul().source === "file" ? "loaded from ~/.half-pi/SOUL.md" : "using built-in default"));
	console.log("[half-pi] Tools registered: " + DEFAULT_TOOLS.join(", "));
	console.log("");
	console.log("[half-pi] The agent loop is ready. LLM integration pending —");
	console.log("[half-pi] configure your API key and model, then re-run.");
}

main().catch((err) => {
	console.error("half-pi error:", err);
	process.exit(1);
});
