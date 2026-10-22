import { createInterface } from "node:readline";
import { Type } from "typebox";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import {
  createAgentSession, createExtensionRuntime,
  ModelRuntime, SessionManager, SettingsManager,
} from "@earendil-works/pi-coding-agent";

export const schemas = [
  { name: "harness_read", description: "Read a permitted source or test file with its hash. Output is bounded.",
    parameters: Type.Object({ file: Type.String() }, { additionalProperties: false }) },
  { name: "harness_search", description: "Find literal text in one permitted file. At most 20 short matches.",
    parameters: Type.Object({ file: Type.String(), text: Type.String({ minLength: 1, maxLength: 200 }) }, { additionalProperties: false }) },
  { name: "harness_edit", description: "Replace one exact match in the allowed source, using its current hash and a unique operation ID.",
    parameters: Type.Object({ file: Type.String(), operation_id: Type.String(), expected_sha256: Type.String(),
      old: Type.String(), new: Type.String() }, { additionalProperties: false }) },
  { name: "harness_check", description: "Run the single trusted import check inside the prepared sandbox.",
    parameters: Type.Object({ preset: Type.Literal("xml-leading-bom") }, { additionalProperties: false }) },
];

export async function runScriptedLoop(start, rpc, emit) {
  const systemPrompt = "Exercise the prepared harness tool sequence. This is a scripted wiring demonstration, not an AI repair. " +
    "Only the four harness tools are available. The runner owns file scope, hashes, budgets and sandboxed tests.";
  const prompt = `Task: ${start.task.id}. ${start.task.expected_behaviour}`;
  const credentials = { async read() {}, async list() { return []; },
    async modify(_provider, fn) { return fn(undefined); }, async delete() {} };
  const runtime = await ModelRuntime.create({ credentials, modelsPath: null,
    allowModelNetwork: false, refreshOnCreate: false });
  let turn = 0;
  runtime.registerProvider("harness-scripted", {
    name: "Prepared responses (no AI)", baseUrl: "https://example.invalid", api: "harness-scripted",
    apiKey: "offline-placeholder",
    models: [{ id: "prepared", name: "Prepared sequence", reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 64000, maxTokens: 2048 }],
    streamSimple(model) {
      const stream = createAssistantMessageEventStream();
      const call = start.calls[turn++];
      const message = { role: "assistant", api: model.api, provider: model.provider, model: model.id,
        content: call ? [{ type: "toolCall", id: `prepared-${turn}`, name: call.name, arguments: call.args }]
          : [{ type: "text", text: "Prepared sequence finished. Human review pending." }],
        stopReason: call ? "toolUse" : "stop", timestamp: Date.now(),
        usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
      stream.push({ type: "start", partial: message });
      stream.push({ type: "done", reason: message.stopReason, message });
      return stream;
    },
  });
  const resourceLoader = {
    getExtensions: () => ({ extensions: [], errors: [], runtime: createExtensionRuntime() }),
    getSkills: () => ({ skills: [], diagnostics: [] }),
    getPrompts: () => ({ prompts: [], diagnostics: [] }),
    getThemes: () => ({ themes: [], diagnostics: [] }),
    getAgentsFiles: () => ({ agentsFiles: [] }),
    getSystemPrompt: () => systemPrompt, getSystemPromptSource: () => undefined,
    getAppendSystemPrompt: () => [], getAppendSystemPromptSources: () => [],
    extendResources() {}, async reload() {},
  };
  let toolResults = 0;
  let invalidOutcome = false;
  const customTools = schemas.map(schema => ({ ...schema, label: schema.name,
    async execute(id, args) {
      if (++toolResults > start.maxToolCalls) throw new Error("Tool budget reached");
      const result = await rpc({ type: "tool", id, name: schema.name, args });
      if (!result || !["ok", "rejected", "passed", "repair_failed"].includes(result.outcome) || typeof result.explanation !== "string" ||
          typeof result.evidence !== "string" || !("operation_id" in result) || !("file" in result)) {
        invalidOutcome = true;
        throw new Error("Invalid runner outcome");
      }
      return { content: [{ type: "text", text: JSON.stringify(result) }], details: result,
        isError: result.outcome === "rejected" || result.outcome === "repair_failed" };
    },
  }));
  const { session } = await createAgentSession({ cwd: process.cwd(), agentDir: process.env.PI_CODING_AGENT_DIR,
    modelRuntime: runtime, model: runtime.getModel("harness-scripted", "prepared"), thinkingLevel: "off",
    resourceLoader, customTools, tools: schemas.map(schema => schema.name),
    sessionManager: SessionManager.inMemory(process.cwd()),
    settingsManager: SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, cacheWarming: "off" }),
  });
  try {
    const active = session.getActiveToolNames().sort();
    if (JSON.stringify(active) !== JSON.stringify(schemas.map(schema => schema.name).sort())) {
      throw new Error("Unexpected active tools");
    }
    emit({ type: "context", systemPrompt: session.agent.state.systemPrompt, prompt, tools: schemas,
      activeTools: active, task: start.task, mode: "scripted-no-model", maxToolCalls: start.maxToolCalls,
      nodeVersion: process.version, permissions: process.permission ? {
        network: process.permission.has("net"), childProcess: process.permission.has("child"),
      } : null });
    await session.prompt(prompt);
    if (invalidOutcome) throw new Error("Invalid runner outcome");
    if (session.agent.state.messages.some(message => message.role === "assistant" &&
        ["error", "aborted"].includes(message.stopReason))) throw new Error("Pi loop did not finish");
    emit({ type: "finished", toolResults, messages: session.agent.state.messages, modelCalls: 0, modelCostUsd: 0 });
  } finally {
    session.dispose();
  }
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  const lines = createInterface({ input: process.stdin });
  const iterator = lines[Symbol.asyncIterator]();
  const emit = value => process.stdout.write(JSON.stringify(value) + "\n");
  try {
    if (!process.permission || process.permission.has("net") || process.permission.has("child")) {
      throw new Error("The scripted loop requires Node permissions with network and child processes denied");
    }
    const first = await iterator.next();
    if (first.done) throw new Error("Missing startup context");
    const start = JSON.parse(first.value);
    await runScriptedLoop(start, async request => {
      emit(request);
      const reply = await iterator.next();
      if (reply.done) throw new Error("Runner disconnected");
      const message = JSON.parse(reply.value);
      if (message.id !== request.id) throw new Error("Runner reply ID mismatch");
      return message.result;
    }, emit);
  } catch {
    process.stderr.write("Pi tool loop failed. No live model transport is enabled.\n");
    process.exitCode = 1;
  } finally {
    lines.close();
  }
}
