/**
 * AgentSession — minimal session wrapper for half-pi.
 *
 * Wraps the pi Agent class, registers tools, builds system prompt with SOUL.md,
 * and manages the prompt → agent run lifecycle.
 */

import { Agent, type AgentEvent, type AgentTool } from "@earendil-works/pi-agent-core";
import { modelsAreEqual, type Model, streamSimple } from "@earendil-works/pi-ai";
import type { ThinkingLevel } from "@earendil-works/pi-agent-core";
import { convertToLlm } from "@earendil-works/pi-agent-core";
import { getSessionsDir } from "../config.ts";
import { loadAllSkills } from "./skills.ts";
import { buildSystemPrompt, type BuildSystemPromptOptions } from "./system-prompt.ts";
import { createHalfPiTools } from "./tool-impls.ts";
import { DEFAULT_TOOLS, TOOL_SNIPPETS, type ToolName } from "./tools.ts";

export interface AgentSessionOptions {
	cwd: string;
	model: Model<any>;
	thinkingLevel?: ThinkingLevel;
	customSystemPrompt?: string;
	enabledTools?: ToolName[];
}

export class AgentSession {
	private agent: Agent;
	private tools: Map<ToolName, AgentTool<ToolName>>;
	private cwd: string;
	private model: Model<any>;
	private thinkingLevel: ThinkingLevel;

	constructor(options: AgentSessionOptions) {
		this.cwd = options.cwd;
		this.model = options.model;
		this.thinkingLevel = options.thinkingLevel ?? "off";

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

		// Create Agent instance
		const activeTools = enabledTools
			.map((name) => this.tools.get(name))
			.filter((t): t is AgentTool<ToolName> => !!t);

		this.agent = new Agent({
			initialState: {
				systemPrompt,
				model: this.model,
				tools: activeTools,
				messages: [],
			},
			streamFn: (ctx, opts, signal) =>
				streamSimple(this.model, convertToLlm(ctx.messages, ctx.tools), {
					...opts,
					thinkingLevel: this.thinkingLevel,
					signal,
				}),
		});
	}

	/** Get the underlying Agent instance */
	getAgent(): Agent {
		return this.agent;
	}

	/** Send a prompt to the agent and wait for completion */
	async prompt(text: string): Promise<string> {
		let finalText = "";

		// Subscribe to events to capture the final response
		const unsubscribe = this.agent.subscribe((event) => {
			if (event.type === "agent_end") {
				// Extract final assistant text from messages
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

		// Wait for agent to finish
		await this.agent.waitForIdle();
		unsubscribe();

		return finalText || "(no response)";
	}

	/** Send a prompt and stream events to a callback */
	async promptWithEvents(
		text: string,
		onEvent: (event: AgentEvent) => void,
	): Promise<void> {
		const unsubscribe = this.agent.subscribe(onEvent);
		await this.agent.prompt(text);
		await this.agent.waitForIdle();
		unsubscribe();
	}

	/** Abort current run */
	abort(): void {
		this.agent.abort();
	}

	/** Get the current system prompt */
	get systemPrompt(): string {
		return this.agent.state.systemPrompt;
	}
}
