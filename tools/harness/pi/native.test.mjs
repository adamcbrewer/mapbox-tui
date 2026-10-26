import assert from "node:assert/strict";
import { test } from "node:test";
import { createReadToolDefinition, createEditToolDefinition, createWriteToolDefinition,
  createBashToolDefinition } from "@earendil-works/pi-coding-agent";
import { createNativeTools, nativeNames, runNativeScriptedLoop } from "./native.mjs";
import { runNativeLoop } from "./native.mjs";
import { createScriptedRuntime } from "./loop.mjs";
import { boundedStream } from "./live.mjs";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

function sandbox() {
  const files = new Map([["/workspace/source.txt", "before\n"]]);
  const requests = [];
  return { files, requests, async rpc(request) {
    requests.push(request);
    if (request.type !== "native_fs") return { ok: true };
    if (request.op === "bash") return { ok: true, value: {
      output: Buffer.from(request.command === "large" ? "x\n".repeat(30000) : "prepared output\n").toString("base64"),
      exitCode: request.command === "fail" ? 7 : 0,
    } };
    if (request.path !== "/workspace" && !request.path.startsWith("/workspace/")) {
      return { ok: false, error: "Outside the project" };
    }
    if (request.op === "mkdir") return { ok: true, value: {} };
    if (request.op === "write") {
      files.set(request.path, request.content);
      return { ok: true, value: {} };
    }
    if (!files.has(request.path)) return { ok: false, error: "Missing project file" };
    return { ok: true, value: request.op === "read"
      ? { data: Buffer.from(files.get(request.path)).toString("base64") } : {} };
  } };
}

test("controller feedback continues the actual SDK session without replacing its tools or context", async () => {
  const remote = sandbox(), emitted = [], checkpoints = [];
  const rpc = async request => {
    if (request.type !== "workflow_checkpoint") return remote.rpc(request);
    checkpoints.push(request);
    return checkpoints.length === 1 ? { action: "continue", prompt: "A supplied check failed. Try the next prepared step." }
      : { action: "stop" };
  };
  await runNativeScriptedLoop({ workflowController: true, task: { id: "continuation" }, calls: [
    { name: "read", args: { path: "source.txt" } }, null,
    { name: "bash", args: { command: "pwd" } }, null,
  ] }, rpc, value => emitted.push(value));
  assert.equal(checkpoints.length, 2);
  assert.equal(checkpoints[0].agentOutcome, "finished");
  assert.equal(checkpoints[1].messages.filter(m => m.role === "toolResult").length, 2);
  assert.ok(checkpoints[1].messages.some(m => m.role === "user" && JSON.stringify(m).includes("supplied check failed")));
  assert.ok(emitted.some(value => value.type === "native_message"));
  assert.equal(emitted.find(value => value.type === "finished").modelCalls, 0);
});

test("a controlled request-budget stop can be checked but cannot continue inference", async () => {
  const start = { workflowController: true, task: { id: "budget" }, prompt: "Read.",
    calls: [{ name: "read", args: { path: "source.txt" } }] };
  const runtime = await createScriptedRuntime(start), model = runtime.getModel("harness-scripted", "prepared");
  const remote = sandbox(), emitted = [], checkpoints = [], requests = { count: 0 };
  const config = { model: model.id, provider: model.provider, maxModelRequests: 1, maxOutputTokensPerRequest: null };
  await runNativeLoop(start, async request => {
    if (request.type !== "workflow_checkpoint") return remote.rpc(request);
    checkpoints.push(request);
    return { action: "stop" };
  }, value => emitted.push(value), { runtime, model, requests,
    streamFunction: boundedStream(runtime, model, config, requests, value => emitted.push(value), () => {}, 1000,
      async request => ({ outcome: "ok", request }), nativeNames) });
  assert.equal(requests.count, 1);
  assert.equal(checkpoints.length, 1);
  assert.equal(checkpoints[0].agentOutcome, "budget_exhausted");
});

test("normal Pi schemas stay intact and file operations go through the sandbox", async () => {
  const remote = sandbox();
  const definitions = createNativeTools(remote.rpc);
  const originals = [createReadToolDefinition, createEditToolDefinition, createWriteToolDefinition, createBashToolDefinition];
  definitions.forEach((tool, index) => assert.deepEqual(tool.parameters, originals[index]("/workspace").parameters));
  const tools = Object.fromEntries(definitions.map(tool => [tool.name, tool]));
  await tools.write.execute("write", { path: "new.txt", content: "hello" });
  await tools.edit.execute("edit", { path: "new.txt", edits: [{ oldText: "hello", newText: "changed" }] });
  const result = await tools.read.execute("read", { path: "new.txt" });
  assert.equal(result.content[0].text, "changed");
  await assert.rejects(tools.read.execute("denied", { path: "/etc/passwd" }), /Outside the project/);
  assert.deepEqual(remote.requests.filter(request => request.type === "native_tool_finished").map(request => Boolean(request.error)),
    [false, false, false, true]);
});

test("a nonzero bash command stays a failed normal tool result", async () => {
  const remote = sandbox();
  const tools = createNativeTools(remote.rpc);
  const result = await tools.find(tool => tool.name === "bash").execute("bad", { command: "fail" });
  assert.equal(result.isError, true);
  assert.match(remote.requests.at(-1).error, /code 7/);
});

test("only SDK-created output files get the local read exception", async () => {
  const remote = sandbox();
  const tools = Object.fromEntries(createNativeTools(remote.rpc).map(tool => [tool.name, tool]));
  const result = await tools.bash.execute("long", { command: "large" });
  assert.ok(result.details.fullOutputPath);
  const read = await tools.read.execute("spill", { path: result.details.fullOutputPath, offset: 29999, limit: 2 });
  assert.match(read.content[0].text, /^x\nx\n/);
  await assert.rejects(tools.edit.execute("spill-edit", { path: result.details.fullOutputPath,
    edits: [{ oldText: "x", newText: "changed" }] }), /Outside the project/);
  await assert.rejects(tools.write.execute("spill-write", { path: result.details.fullOutputPath, content: "changed" }), /Outside the project/);
  await assert.rejects(tools.read.execute("host", { path: process.env.HOME + "/.pi/agent/auth.json" }), /Outside the project/);
});

test("the real SDK session selects normal tools and retains intentional errors", async () => {
  const remote = sandbox();
  const emitted = [];
  const savedFetch = globalThis.fetch;
  globalThis.fetch = () => { throw new Error("No model network calls in this check"); };
  try {
    await runNativeScriptedLoop({ task: { id: "native-tools" }, calls: [
      { name: "read", args: { path: "source.txt" } }, { name: "bash", args: { command: "fail" } },
    ] }, remote.rpc, value => emitted.push(value));
  } finally {
    globalThis.fetch = savedFetch;
  }
  assert.deepEqual(emitted[0].activeTools.sort(), [...nativeNames].sort());
  const results = emitted.at(-1).messages.filter(message => message.role === "toolResult");
  assert.equal(results.length, 2);
  assert.equal(results[0].content[0].text, "before\n");
  assert.equal(results[1].isError, true);
  assert.equal(emitted.at(-1).modelCalls, 0);
});

test("bounded live requests use normal tools through the real SDK without network", async () => {
  const start = { task: { id: "live-native" }, prompt: "Repair the supplied task.", calls: [
    { name: "read", args: { path: "source.txt" } }, { name: "bash", args: { command: "fail" } },
  ] };
  const runtime = await createScriptedRuntime(start);
  const model = runtime.getModel("harness-scripted", "prepared");
  const remote = sandbox(), emitted = [], reservations = [];
  const requests = { count: 0 };
  const config = { provider: model.provider, model: model.id, auth: "subscription", maxModelRequests: 3, maxOutputTokensPerRequest: null };
  await runNativeLoop(start, remote.rpc, value => emitted.push(value), { runtime, model, requests,
    streamFunction: boundedStream(runtime, model, config, requests, value => emitted.push(value), () => {}, 1000,
      async request => { reservations.push(request); return { outcome: "ok", request }; }, nativeNames) });
  assert.deepEqual(reservations, [1, 2, 3]);
  const context = emitted.find(value => value.type === "context");
  assert.match(context.systemPrompt, /expert coding assistant operating inside pi/);
  assert.deepEqual(context.activeTools.sort(), [...nativeNames].sort());
  assert.equal(emitted.find(value => value.type === "finished").modelCalls, 3);
  assert.equal(emitted.at(-1).messages.filter(message => message.role === "toolResult")[1].isError, true);
});

test("native live request exhaustion cannot emit a successful finish", async () => {
  const start = { task: { id: "limited" }, prompt: "Repair.", calls: [{ name: "read", args: { path: "source.txt" } }] };
  const runtime = await createScriptedRuntime(start);
  const model = runtime.getModel("harness-scripted", "prepared");
  const requests = { count: 0 }, emitted = [], remote = sandbox();
  const config = { provider: model.provider, model: model.id, maxModelRequests: 1, maxOutputTokensPerRequest: null };
  await assert.rejects(runNativeLoop(start, remote.rpc, value => emitted.push(value), { runtime, model, requests,
    streamFunction: boundedStream(runtime, model, config, requests, value => emitted.push(value), () => {}, 1000,
      async request => ({ outcome: "ok", request }), nativeNames) }), /budget reached/);
  assert.equal(requests.count, 1);
  assert.equal(emitted.some(value => value.type === "finished"), false);
  assert.equal(emitted.at(-1).type, "session");
});

test("native payload scope accepts normal tools and refuses an extra tool before transport", async () => {
  const model = { provider: "test", id: "chosen", api: "test" };
  const config = { model: "chosen", maxModelRequests: 1, maxOutputTokensPerRequest: null };
  for (const extra of [false, true]) {
    const requests = { count: 0 }, emitted = [];
    const runtime = { streamSimple(_model, _context, options) {
      options.onPayload({ model: "chosen", tools: [...nativeNames, ...(extra ? ["harness_edit"] : [])].map(name => ({ name })) });
      const stream = createAssistantMessageEventStream();
      stream.push({ type: "done", reason: "stop", message: { role: "assistant", content: [], stopReason: "stop" } });
      return stream;
    } };
    const stream = boundedStream(runtime, model, config, requests, value => emitted.push(value), () => {}, 1000, null, nativeNames);
    const events = [];
    for await (const event of stream(model, {}, {})) events.push(event);
    assert.equal(events.at(-1).type, extra ? "error" : "done");
    assert.equal(emitted.some(value => value.type === "model_payload_rejected"), false);
    assert.equal(Boolean(requests.failure), extra);
  }
});
