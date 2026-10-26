import assert from "node:assert/strict";
import { test } from "node:test";
import { createReadToolDefinition, createEditToolDefinition, createWriteToolDefinition,
  createBashToolDefinition } from "@earendil-works/pi-coding-agent";
import { createNativeTools, nativeNames, runNativeScriptedLoop } from "./native.mjs";

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
