/**
 * AgentSession — session wrapper for half-pi.
 *
 * In v4, the LLM is a director that controls souls via `speak(soul, text)`.
 * The LLM does NOT roleplay as any soul — it directs them.
 *
 * speak(soul, text) is the ONLY way the LLM outputs text.
 * pass_soul is no longer needed — the director switches souls in speak directly.
 */

import { Agent, type AgentEvent } from "@earendil-works/pi-agent-core";
import type { AgentTool } from "@earendil-works/pi-agent-core";
import type { Model } from "@earendil-works/pi-ai";
import type { ThinkingLevel } from "@earendil-works/pi-agent-core";
import { loadAllSkills } from "./skills.js";
import { buildSystemPrompt, type BuildSystemPromptOptions } from "./system-prompt.js";
import { createHalfPiTools } from "./tool-impls.js";
import { DEFAULT_TOOLS, TOOL_SNIPPETS, type ToolName } from "./tools.js";
import { loadGroup } from "./groups.js";
/** Max consecutive soul switches before forcing user input (base) */
const MAX_SPEAK_TURNS_BASE = 4;

/** Random jitter added to the limit (0 to this value) */
const MAX_SPEAK_TURNS_JITTER = 2;

function getMaxSpeakTurns(): number {
	return MAX_SPEAK_TURNS_BASE + Math.floor(Math.random() * (MAX_SPEAK_TURNS_JITTER + 1));
}

export interface AgentSessionOptions {
	cwd: string;
	model: Model<any>;
	thinkingLevel?: ThinkingLevel;
	customSystemPrompt?: string;
	enabledTools?: ToolName[];
	onEvent?: (event: AgentEvent) => void;
	confirmDangerous?: (toolName: string, params: Record<string, unknown>) => Promise<boolean>;
	groupName?: string;
	soulNames?: string[];
	onSoulSwitch?: (name: string) => void;
	/**
	 * Called when speak tool produces text. Should write to stdout.
	 * Arguments: (soulName, text)
	 */
	onSpeakDisplay?: (soul: string, text: string) => void;
}

export class AgentSession {
	private agent: Agent;
	private tools: Map<ToolName, AgentTool>;
	private cwd: string;
	private eventHandler?: (event: AgentEvent) => void;

	public groupSouls: string[] = [];
	public currentSoul: string = "zero";
	public speakCount: number = 0;
	private onSoulSwitch?: (name: string) => void;
	private onSpeakDisplay?: (soul: string, text: string) => void;
	private lastSpeakSoul: string = "";

	constructor(options: AgentSessionOptions) {
		this.cwd = options.cwd;
		this.eventHandler = options.onEvent;
		this.onSoulSwitch = options.onSoulSwitch;
		this.onSpeakDisplay = options.onSpeakDisplay;

		if (options.groupName) {
			const group = loadGroup(options.groupName);
			if (group) {
				this.groupSouls = group.souls;
				this.currentSoul = group.defaultSoul ?? group.souls[0];
			}
		} else if (options.soulNames && options.soulNames.length > 0) {
			this.groupSouls = options.soulNames;
			this.currentSoul = options.soulNames[0];
		}

		this.tools = createHalfPiTools(this.cwd, {
			onSpeak: (soul, text) => this.handleSpeak(soul, text),
		});

		const enabledTools = options.enabledTools ?? DEFAULT_TOOLS;
		const systemPrompt = buildSystemPrompt({
			cwd: this.cwd,
			customPrompt: options.customSystemPrompt,
			selectedTools: enabledTools,
			toolSnippets: TOOL_SNIPPETS,
			skills: loadAllSkills(),
			soulNames: this.groupSouls.length > 0 ? this.groupSouls : undefined,
		});

		const activeTools = enabledTools
			.map((name: ToolName) => this.tools.get(name))
			.filter((t): t is AgentTool => !!t);

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
						if (ctx.toolCall.name !== "bash") return;
						const ok = await options.confirmDangerous!(ctx.toolCall.name, ctx.args as Record<string, unknown>);
						if (!ok) return { block: true, reason: "User denied the dangerous command." };
					}
				: undefined,
		});
	}

	/**
	 * Handle speak tool call.
	 * Returns the text to display. Validates soul, tracks turn count.
	 */
	public handleSpeak(soul: string, text: string): string {
		// Validate soul
		if (!this.groupSouls.includes(soul)) {
			return `[system: 无效 soul "${soul}"，可用: ${this.groupSouls.join(", ")}]`;
		}

		// Count soul switches, not absolute calls
		if (this.lastSpeakSoul && soul !== this.lastSpeakSoul) {
			this.speakCount++;
		}
		this.lastSpeakSoul = soul;

		// Check limit
		const limit = getMaxSpeakTurns();
		if (this.speakCount > limit) {
			this.speakCount = 0;
			return "[system: 已连续多次对话，请等待用户输入。]";
		}

		this.currentSoul = soul;
		this.onSoulSwitch?.(soul);

		// Direct display
		this.onSpeakDisplay?.(soul, text);

		return text;
	}

	getAgent(): Agent {
		return this.agent;
	}

	async prompt(text: string): Promise<string> {
		// User intervention resets the counter
		this.speakCount = 0;
		this.lastSpeakSoul = "";

		let finalText = "";
		const unsubscribe = this.agent.subscribe((event: AgentEvent) => {
			this.eventHandler?.(event);

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

	abort(): void {
		this.agent.abort();
	}

	get systemPrompt(): string {
		return this.agent.state.systemPrompt;
	}
}
