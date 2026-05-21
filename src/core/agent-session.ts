/**
 * AgentSession — session wrapper for half-pi.
 *
 * Wraps the pi Agent class, registers tools, builds system prompt with SOUL.md,
 * and manages the prompt → agent run lifecycle.
 */

import { Agent, type AgentEvent } from "@earendil-works/pi-agent-core";
import type { AgentTool } from "@earendil-works/pi-agent-core";
import type { Model } from "@earendil-works/pi-ai";
import type { ThinkingLevel } from "@earendil-works/pi-agent-core";
import { loadAllSkills } from "./skills.js";
import { buildSystemPrompt, type BuildSystemPromptOptions } from "./system-prompt.js";
import { createHalfPiTools } from "./tool-impls.js";
import { DEFAULT_TOOLS, TOOL_SNIPPETS, type ToolName } from "./tools.js";

export interface AgentSessionOptions {
	cwd: string;
	model: Model<any>;
	thinkingLevel?: ThinkingLevel;
	customSystemPrompt?: string;
	enabledTools?: ToolName[];
	/** Optional event callback for streaming output */
	onEvent?: (event: AgentEvent) => void;
	/**
	 * Called before executing a tool. Return false to block execution.
	 * Only called for the "bash" tool currently.
	 */
	confirmDangerous?: (toolName: string, params: Record<string, unknown>) => Promise<boolean>;
}

export class AgentSession {
	private agent: Agent;
	private tools: Map<ToolName, AgentTool>;
	private cwd: string;
	private eventHandler?: (event: AgentEvent) => void;

	constructor(options: AgentSessionOptions) {
		this.cwd = options.cwd;
		this.eventHandler = options.onEvent;

		// Create tools
		this.tools = createHalfPiTools(this.cwd);

		// Build system prompt
		const enabledTools = options.enabledTools ?? DEFAULT_TOOLS;
		const systemPrompt = buildSystemPrompt({
			cwd: this.cwd,
			customPrompt: options.customSystemPrompt,
			selectedTools: enabledTools,
			toolSnippets: TOOL_SNIPPETS,
			skills: loadAllSkills(),
		});

		// Gather active tools
		const activeTools = enabledTools
			.map((name: ToolName) => this.tools.get(name))
			.filter((t): t is AgentTool => !!t);

		// Create Agent — use pi's built-in convertToLlm and streamFn defaults
		this.agent = new Agent({
			initialState: {
				systemPrompt,
				model: options.model,
				tools: activeTools,
				messages: [],
			},
			thinkingBudgets: {
				low: 1024,
				medium: 4096,
				high: 16384,
			},
			beforeToolCall: options.confirmDangerous
				? async (ctx) => {
						// Only intercept bash
						if (ctx.toolCall.name !== "bash") return;
						const ok = await options.confirmDangerous!(ctx.toolCall.name, ctx.args as Record<string, unknown>);
						if (!ok) return { block: true, reason: "User denied the dangerous command." };
					}
				: undefined,
		});
	}

	/** Get the underlying Agent instance */
	getAgent(): Agent {
		return this.agent;
	}

	/**
	 * Send a prompt and wait for the agent to complete.
	 * Events (text chunks, tool calls) are forwarded to the onEvent callback
	 * if provided in constructor options.
	 */
	async prompt(text: string): Promise<string> {
		let finalText = "";

		const unsubscribe = this.agent.subscribe((event: AgentEvent) => {
			// Forward to user callback
			this.eventHandler?.(event);

			// Track final text from agent_end
			if (event.type === "agent_end") {
				const messages = this.agent.state.messages;
				for (let i = messages.length - 1; i >= 0; i--) {
					const msg = messages[i];
					if (msg.role === "assistant") {
						for (const block of msg.content) {
							if (block.type === "text") {
								finalText = block.text;
							}
						}
						break;
					}
				}
			}
		});

		await this.agent.prompt(text);
		await this.agent.waitForIdle();
		unsubscribe();

		return finalText || "(no response)";
	}

	/** Abort current run */
	abort(): void {
		this.agent.abort();
	}

	get systemPrompt(): string {
		return this.agent.state.systemPrompt;
	}
}
