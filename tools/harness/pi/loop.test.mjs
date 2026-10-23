import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { copyFileSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { ModelRuntime } from "@earendil-works/pi-coding-agent";
import { runScriptedLoop, runToolLoop, schemas } from "./loop.mjs";
import { boundedStream } from "./live.mjs";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { liveReadiness, prepareLoginOptions, requireSubscription, resolveLiveModel,
  setupErrorMessage, validateConfig } from "./setup.mjs";

test("real SDK uses only the four explicit tools and returns structured refusals", async () => {
  const sent = [], requests = [];
  const savedFetch = globalThis.fetch;
  globalThis.fetch = () => { throw new Error("Network must not be used"); };
  try {
    await runScriptedLoop({ task: { id: "offline-contract", expected_behaviour: "Exercise tools" }, maxToolCalls: 4,
      repoInstructions: [{ file: "AGENTS.md", sha256: "approved-hash", text: "Preserve import compatibility." }],
      calls: [{ name: "harness_read", args: { file: "/etc/passwd" } },
        { name: "harness_check", args: { preset: "xml-leading-bom" } }] }, async request => {
      requests.push(request);
      return { outcome: request.name === "harness_read" ? "rejected" : "passed", explanation: "Prepared outcome",
        evidence: "events.jsonl", file: null, operation_id: null };
    }, value => sent.push(value));
  } finally {
    globalThis.fetch = savedFetch;
  }
  assert.deepEqual(requests.map(request => request.name), ["harness_read", "harness_check"]);
  assert.deepEqual(sent[0].activeTools, schemas.map(schema => schema.name).sort());
  assert.equal(sent[0].mode, "scripted-no-model");
  assert.match(sent[0].systemPrompt, /Preserve import compatibility/);
  assert.match(sent[0].systemPrompt, /References to other files or workflows do not expand those limits/);
  assert.deepEqual(sent[0].repoInstructions,
    [{ file: "AGENTS.md", sha256: "approved-hash", text: "Preserve import compatibility." }]);
  assert.equal(sent.at(-1).modelCalls, 0);
  assert.equal(sent.at(-1).toolResults, 2);
  assert.equal(sent.at(-1).messages.filter(message => message.role === "toolResult").length, 2);
});

test("missing structured outcome prevents a successful loop", async () => {
  await assert.rejects(runScriptedLoop({ task: { id: "bad" }, maxToolCalls: 1,
    calls: [{ name: "harness_read", args: { file: "go/import.go" } }] }, async () => ({}), () => {}));
});

test("subscription selection fails closed for API keys and unsupported providers", () => {
  const provider = { auth: { oauth: { isSubscription: true } } };
  assert.throws(() => requireSubscription(provider, { type: "api_key" }));
  assert.throws(() => requireSubscription({ auth: {} }, { type: "oauth" }));
  assert.throws(() => requireSubscription(provider, undefined));
  assert.doesNotThrow(() => requireSubscription(provider, { type: "oauth" }));
});

test("local model and request limits are explicit; extra settings and API mode fail", () => {
  const config = { provider: "openai", model: "chosen-locally", auth: "subscription",
    maxOutputTokensPerRequest: 1024, maxModelRequests: 4 };
  assert.equal(validateConfig(config), config);
  assert.equal(validateConfig({ ...config, maxOutputTokensPerRequest: null }).maxOutputTokensPerRequest, null);
  for (const invalid of [{ ...config, model: null }, { ...config, auth: "api_key" },
    { ...config, provider: "anthropic" },
    { ...config, maxModelRequests: 0 }, { ...config, maxOutputTokensPerRequest: 8193 },
    { ...config, apiKey: "must-not-be-here" }]) assert.throws(() => validateConfig(invalid));
});

test("local setup preserves the chosen OpenAI subscription model", async () => {
  const credentials = { async read() {}, async list() { return []; },
    async modify(_provider, fn) { return fn(undefined); }, async delete() {} };
  const runtime = await ModelRuntime.create({ credentials, modelsPath: null,
    allowModelNetwork: false, refreshOnCreate: false });
  const temporary = mkdtempSync(join(tmpdir(), "mapbox-pi-selection-"));
  try {
    copyFileSync(new URL("./setup.mjs", import.meta.url), join(temporary, "setup.mjs"));
    symlinkSync(new URL("./node_modules", import.meta.url), join(temporary, "node_modules"), "dir");
    for (const provider of ["openai"]) {
      assert.equal(runtime.getProvider(provider).auth.oauth.isSubscription, true);
      const models = runtime.getModels(provider).slice(0, 2);
      assert.equal(models.length, 2);
      for (const model of models) {
        writeFileSync(join(temporary, "config.local.json"), JSON.stringify({ provider, model: model.id,
          auth: "subscription", maxOutputTokensPerRequest: 1024, maxModelRequests: 4 }));
        const output = execFileSync(process.execPath, [join(temporary, "setup.mjs"), "status"], {
          encoding: "utf8", env: { HOME: temporary, PI_OFFLINE: "1" },
        });
        const status = JSON.parse(output);
        assert.equal(status.provider, provider);
        assert.equal(status.model, model.id);
        assert.equal(status.auth, "subscription");
        assert.ok(status.models.includes(model.id));
        assert.equal(status.signedIn, false);
        assert.equal(status.modelCalls, 0);
        assert.equal(status.liveExecutionEnabled, false);
      }
    }
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});

test("live readiness requires the exact catalogue model and supported transport without a model call", () => {
  const config = { provider: "openai", model: "chosen-locally", auth: "subscription",
    maxOutputTokensPerRequest: null, maxModelRequests: 4 };
  const model = { id: config.model, api: "openai-responses", provider: "openai" };
  let selected = model;
  const runtime = {
    getModel(provider, id) {
      assert.equal(provider, config.provider);
      assert.equal(id, config.model);
      return selected;
    },
    streamSimple() { assert.fail("Readiness must not call a model"); },
  };
  assert.equal(resolveLiveModel(runtime, config), model);
  assert.deepEqual(liveReadiness(runtime, config, true), { liveExecutionEnabled: true, liveBlocker: null });
  for (selected of [undefined, { ...model, api: "openai-completions" }]) {
    assert.throws(() => resolveLiveModel(runtime, config), /selected model was not substituted/);
    const status = liveReadiness(runtime, config, true);
    assert.equal(status.liveExecutionEnabled, false);
    assert.match(status.liveBlocker, /available openai subscription Responses model/);
  }
  selected = model;
  assert.match(liveReadiness(runtime, config, false).liveBlocker, /sign Pi in/);
  assert.match(liveReadiness(runtime, { ...config, maxOutputTokensPerRequest: 1024 }, true).liveBlocker,
    /hard output cap cannot be enforced/);
  assert.equal(liveReadiness(runtime, { ...config, model: null }, true).liveExecutionEnabled, false);
  assert.equal(liveReadiness(runtime, { ...config, maxModelRequests: null }, true).liveExecutionEnabled, false);
});

test("sign-in options persist one Pi installation ID and preserve existing settings", async () => {
  const temporary = mkdtempSync(join(tmpdir(), "mapbox-pi-login-"));
  try {
    const first = await prepareLoginOptions(temporary);
    assert.match(first.getDeviceId(), /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i);
    assert.equal(first.agentName, "Mapbox TUI harness");
    const path = join(temporary, "settings.json");
    const saved = JSON.parse(readFileSync(path, "utf8"));
    assert.equal(saved.deviceId, first.getDeviceId());
    saved.defaultModel = "keep-my-model";
    writeFileSync(path, JSON.stringify(saved));
    const second = await prepareLoginOptions(temporary);
    assert.equal(second.getDeviceId(), first.getDeviceId());
    assert.equal(JSON.parse(readFileSync(path, "utf8")).defaultModel, "keep-my-model");
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});

test("damaged Pi settings stop login without replacing the installation identity", async () => {
  const temporary = mkdtempSync(join(tmpdir(), "mapbox-pi-login-"));
  try {
    const path = join(temporary, "settings.json");
    writeFileSync(path, "{broken");
    await assert.rejects(prepareLoginOptions(temporary), /installation ID/);
    assert.equal(readFileSync(path, "utf8"), "{broken");
    writeFileSync(path, JSON.stringify({ deviceId: "invalid" }));
    await assert.rejects(prepareLoginOptions(temporary), /installation ID/);
    assert.equal(JSON.parse(readFileSync(path, "utf8")).deviceId, "invalid");
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});

test("login diagnostics explain known failures without exposing token responses or callback URLs", () => {
  assert.match(setupErrorMessage(new Error("Sign in with ChatGPT requires a device ID (UUID) for this installation")), /installation ID/);
  assert.match(setupErrorMessage(new Error("Port 1455 is in use, probably by an unfinished login")), /1455.*busy/);
  assert.match(setupErrorMessage(new Error('OpenAI OAuth token request failed (401): {"access_token":"private-token"}')), /HTTP 401/);
  for (const error of [new Error('OpenAI OAuth token request failed (401): {"access_token":"private-token"}'),
    new Error("https://callback.invalid/?code=private-token"),
    new Error("private-token", { cause: Object.assign(new Error("private-token"), { code: "ENOTFOUND" }) })]) {
    assert.ok(!setupErrorMessage(error).includes("private-token"));
  }
});

async function preparedLive(replies, maxModelRequests = 10) {
  const sent = [], options = [], requests = { count: 0 };
  const runtime = await ModelRuntime.create({ modelsPath: null, allowModelNetwork: false,
    refreshOnCreate: false, credentials: { async read() {}, async list() { return []; } } });
  runtime.registerProvider("live-test", { name: "Offline live-path test", api: "live-test",
    baseUrl: "https://example.invalid", apiKey: "offline-placeholder",
    models: [{ id: "prepared", name: "Prepared", reasoning: false, input: ["text"],
      contextWindow: 64000, maxTokens: 8192,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
    streamSimple(model, _context, requestOptions) {
      options.push(requestOptions);
      const next = replies.shift();
      assert.ok(next, "No unexpected provider request");
      const message = { role: "assistant", api: model.api, provider: model.provider, model: model.id,
        timestamp: Date.now(), usage: { input: 10, output: 5, cacheRead: 0, cacheWrite: 0, totalTokens: 15,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, ...next };
      const stream = createAssistantMessageEventStream();
      stream.push(message.stopReason === "error"
        ? { type: "error", reason: "error", error: message }
        : { type: "done", reason: message.stopReason, message });
      return stream;
    } });
  const model = runtime.getModel("live-test", "prepared");
  const config = { provider: "live-test", model: "prepared", auth: "subscription",
    maxOutputTokensPerRequest: 1024, maxModelRequests };
  const emit = value => sent.push(value);
  const streamFunction = boundedStream(runtime, model, config, requests, emit, () => {});
  return { sent, options, requests, runtime, model, config, emit, streamFunction, thinkingLevel: "low" };
}

function toolReply(name, args, id = name) {
  return { content: [{ type: "toolCall", id, name, arguments: args }], stopReason: "toolUse" };
}

test("live path lets the SDK choose tools and enforces request options without network", async () => {
  const execution = await preparedLive([
    toolReply("harness_read", { file: "/etc/passwd" }),
    toolReply("harness_check", { preset: "xml-leading-bom" }),
    { content: [{ type: "text", text: "The check passed; human review pending." }], stopReason: "stop" },
  ]);
  const called = [];
  await runToolLoop({ task: { id: "live-path", expected_behaviour: "Repair" }, maxToolCalls: 12 },
    async request => {
      called.push(request.name);
      return { outcome: request.name === "harness_read" ? "rejected" : "passed",
        explanation: "Prepared test result", evidence: "events.jsonl", file: null, operation_id: null };
    }, execution.emit, execution);
  assert.deepEqual(called, ["harness_read", "harness_check"]);
  assert.equal(execution.requests.count, 3);
  assert.equal(execution.sent.find(event => event.type === "context").mode, "live-subscription");
  assert.equal(execution.sent.at(-1).modelCostUsd, null);
  for (const options of execution.options) {
    assert.equal(options.maxTokens, 1024);
    assert.equal(options.maxRetries, 0);
    assert.equal(options.reasoning, "low");
    assert.equal(options.transport, "sse");
    const payload = { model: "prepared", max_output_tokens: 1024, tools: schemas };
    assert.doesNotThrow(() => options.onPayload(payload));
    assert.throws(() => options.onPayload({ ...payload, max_output_tokens: 1025 }));
    assert.throws(() => options.onPayload({ ...payload, tools: [...schemas, { name: "bash" }] }));
    assert.throws(() => options.onPayload({ ...payload, model: "different-model" }));
  }
});

test("request exhaustion stops before another provider call", async () => {
  const execution = await preparedLive([toolReply("harness_read", { file: "go/import.go" })], 1);
  let tools = 0;
  await assert.rejects(runToolLoop({ task: { id: "budget" }, maxToolCalls: 12 }, async () => {
    tools++;
    return { outcome: "ok", explanation: "Read", evidence: "events.jsonl", file: "go/import.go", operation_id: null };
  }, execution.emit, execution), error => error.outcome === "budget_exhausted");
  assert.equal(tools, 1);
  assert.equal(execution.options.length, 1);
});

test("verification feedback stays in the same session with a shared request budget", async () => {
  for (const limit of [4, 3]) {
    const execution = await preparedLive([
      toolReply("harness_check", { preset: "xml-leading-bom" }, "first-check"),
      toolReply("harness_edit", { file: "go/import.go" }, "retry-edit"),
      toolReply("harness_check", { preset: "xml-leading-bom" }, "second-check"),
      { content: [{ type: "text", text: "All checks passed." }], stopReason: "stop" },
    ], limit);
    let checks = 0;
    const work = runToolLoop({ task: { id: "verified-retry" }, maxToolCalls: 12,
      verification: { maxAttempts: 2, scope: "permitted-files-v1" } }, async request => {
      const failed = request.name === "harness_check" && ++checks === 1;
      return { outcome: request.name === "harness_edit" ? "ok" : failed ? "repair_failed" : "passed",
        explanation: "Independent result", evidence: "result.json", file: null, operation_id: null,
        remaining_attempts: failed ? 1 : 0 };
    }, execution.emit, execution);
    if (limit === 4) await work;
    else await assert.rejects(work, error => error.outcome === "budget_exhausted");
    const context = execution.sent.find(event => event.type === "context");
    assert.match(context.systemPrompt, /at most two attempts total/);
    assert.match(context.tools.find(tool => tool.name === "harness_check").description, /independently check a fresh source copy/);
    assert.equal(execution.requests.count, limit);
    assert.equal(checks, 2);
    assert.equal(execution.sent.some(event => event.type === "finished"), limit === 4);
    const session = execution.sent.find(event => event.type === "session");
    assert.ok(session.messages.some(message => message.role === "toolResult" &&
      message.content.some(part => part.text?.includes('"remaining_attempts":1'))));
  }
});

test("full-response deadline stops an unfinished stream even after it starts", async () => {
  const execution = await preparedLive([]);
  let signal;
  execution.runtime.streamSimple = (_model, _context, options) => {
    signal = options.signal;
    const stream = createAssistantMessageEventStream();
    stream.push({ type: "start", partial: {} });
    return stream;
  };
  execution.streamFunction = boundedStream(execution.runtime, execution.model, execution.config,
    execution.requests, execution.emit, () => {}, 25);
  let calls = 0;
  await assert.rejects(runToolLoop({ task: { id: "stalled-response" }, maxToolCalls: 12 },
    async () => { calls++; }, execution.emit, execution), error => error.outcome === "timeout");
  assert.equal(signal.aborted, true);
  assert.equal(calls, 0);
  assert.equal(execution.requests.count, 1);
  assert.ok(!execution.sent.some(event => event.type === "finished"));
});

test("truncated tool calls and provider failures cannot execute or expose private diagnostics", async () => {
  for (const reply of [
    { ...toolReply("harness_edit", { file: "go/import.go" }), stopReason: "length" },
    { content: [], stopReason: "error", errorMessage: "401 private-token" },
  ]) {
    const execution = await preparedLive([reply]);
    let tools = 0;
    await assert.rejects(runToolLoop({ task: { id: "failed" }, maxToolCalls: 12 }, async () => {
      tools++;
    }, execution.emit, execution));
    assert.equal(tools, 0);
    assert.ok(!JSON.stringify(execution.sent).includes("private-token"));
    assert.ok(!execution.sent.some(event => event.type === "finished"));
  }
});

test("real live CLI reaches the subscription guard without hanging or contacting a model", () => {
  const temporary = mkdtempSync(join(tmpdir(), "mapbox-pi-live-start-"));
  try {
    const packagePath = new URL(".", import.meta.url).pathname;
    const result = spawnSync(process.execPath, ["--permission", "--allow-net",
      `--allow-fs-read=${packagePath}`, `--allow-fs-read=${temporary}`, `--allow-fs-write=${temporary}`,
      new URL("./cli.mjs", import.meta.url).pathname], {
      cwd: temporary, encoding: "utf8", timeout: 5000,
      env: { HOME: temporary, PI_OFFLINE: "1", PI_CODING_AGENT_DIR: temporary },
      input: JSON.stringify({ mode: "live", authDir: temporary, task: { id: "startup" }, maxToolCalls: 12,
        config: { provider: "openai", model: "gpt-6.1-sol", auth: "subscription",
          maxModelRequests: 1, maxOutputTokensPerRequest: 1024 } }) + "\n",
    });
    assert.equal(result.error, undefined);
    assert.equal(result.status, 1);
    const outcome = JSON.parse(result.stdout);
    assert.equal(outcome.type, "failed");
    assert.match(outcome.reason, /Subscription sign-in is required/);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});

test("actual OpenAI subscription adapter drops the hard cap and our guard stops before fetch", async () => {
  const credential = { type: "oauth", access: "dummy-subscription-token", refresh: "dummy-refresh",
    expires: Date.now() + 3600000 };
  const runtime = await ModelRuntime.create({ modelsPath: null, allowModelNetwork: false, refreshOnCreate: false,
    credentials: { async read(provider) { return provider === "openai" ? credential : undefined; },
      async list() { return [{ providerId: "openai", type: "oauth" }]; } } });
  const model = runtime.getModel("openai", "gpt-6.1-sol");
  const config = { provider: "openai", model: model.id, auth: "subscription",
    maxModelRequests: 1, maxOutputTokensPerRequest: 4096 };
  const sent = [], requests = { count: 0 };
  const savedFetch = globalThis.fetch;
  let fetches = 0;
  globalThis.fetch = async () => { fetches++; throw new Error("Unexpected network"); };
  try {
    const stream = boundedStream(runtime, model, config, requests, event => sent.push(event), () => {});
    const result = await stream(model, { messages: [{ role: "user", content: "No inference", timestamp: Date.now() }] }).result();
    assert.equal(result.stopReason, "error");
    assert.match(requests.failure.message, /omitted.*output-token cap/);
    assert.equal(fetches, 0);
    assert.ok(sent.some(event => event.type === "model_payload_rejected"));
  } finally {
    globalThis.fetch = savedFetch;
  }
});

test("explicitly uncapped subscription payload keeps model/tool guards and records raw usage", async () => {
  const execution = await preparedLive([{ content: [{ type: "text", text: "Done" }], stopReason: "stop" }]);
  execution.config.maxOutputTokensPerRequest = null;
  const original = execution.runtime.streamSimple.bind(execution.runtime);
  execution.runtime.streamSimple = (model, context, options) => {
    assert.equal(options.maxTokens, undefined);
    const payload = { model: model.id, tools: schemas };
    assert.doesNotThrow(() => options.onPayload(payload));
    assert.throws(() => options.onPayload({ ...payload, model: "other" }));
    assert.throws(() => options.onPayload({ ...payload, tools: [{ name: "bash" }] }));
    execution.requests.failure = undefined;
    options.onProviderStreamEvent({ type: "response.completed", response: { usage: {
      input_tokens: 120, input_tokens_details: { cached_tokens: 20 },
      output_tokens: 5000, output_tokens_details: { reasoning_tokens: 3000 }, total_tokens: 5120,
    } } });
    return original(model, context, options);
  };
  await runToolLoop({ task: { id: "uncapped" }, maxToolCalls: 12 }, async () => {}, execution.emit, execution);
  const usage = execution.sent.find(event => event.type === "model_usage").usage;
  assert.deepEqual(usage, { inputTokens: 120, outputTokens: 5000, cachedInputTokens: 20,
    reasoningTokens: 3000, totalTokens: 5120 });
  assert.equal(execution.options.length, 1);
  assert.equal(execution.options[0].timeoutMs, 120000);
  assert.equal(execution.options[0].maxRetries, 0);
});
