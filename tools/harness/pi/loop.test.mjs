import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { copyFileSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { ModelRuntime } from "@earendil-works/pi-coding-agent";
import { runScriptedLoop, schemas } from "./loop.mjs";
import { prepareLoginOptions, requireSubscription, setupErrorMessage, validateConfig } from "./setup.mjs";

test("real SDK uses only the four explicit tools and returns structured refusals", async () => {
  const sent = [], requests = [];
  const savedFetch = globalThis.fetch;
  globalThis.fetch = () => { throw new Error("Network must not be used"); };
  try {
    await runScriptedLoop({ task: { id: "offline-contract", expected_behaviour: "Exercise tools" }, maxToolCalls: 4,
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
  for (const invalid of [{ ...config, model: null }, { ...config, auth: "api_key" },
    { ...config, maxModelRequests: 0 }, { ...config, maxOutputTokensPerRequest: 8193 },
    { ...config, apiKey: "must-not-be-here" }]) assert.throws(() => validateConfig(invalid));
});

test("local setup preserves the chosen subscription provider and model for Codex and Claude", async () => {
  const credentials = { async read() {}, async list() { return []; },
    async modify(_provider, fn) { return fn(undefined); }, async delete() {} };
  const runtime = await ModelRuntime.create({ credentials, modelsPath: null,
    allowModelNetwork: false, refreshOnCreate: false });
  const temporary = mkdtempSync(join(tmpdir(), "mapbox-pi-selection-"));
  try {
    copyFileSync(new URL("./setup.mjs", import.meta.url), join(temporary, "setup.mjs"));
    symlinkSync(new URL("./node_modules", import.meta.url), join(temporary, "node_modules"), "dir");
    for (const provider of ["openai", "anthropic"]) {
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
