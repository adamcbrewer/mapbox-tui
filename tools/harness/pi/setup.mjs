import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { createInterface } from "node:readline/promises";
import { pathToFileURL } from "node:url";
import {
  ModelRuntime,
  readStoredCredential,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";

export class SetupError extends Error {}

export function validateConfig(config, requireModel = true) {
  if (
    !config ||
    typeof config !== "object" ||
    Array.isArray(config) ||
    Object.keys(config).sort().join() !== "auth,maxModelRequests,maxOutputTokensPerRequest,model,provider"
  ) {
    throw new SetupError("Use the five settings in config.example.json");
  }

  if (config.auth !== "subscription") {
    throw new SetupError("This checkpoint requires subscription sign-in; API billing is disabled");
  }
  if (config.provider !== "openai") {
    throw new SetupError("This pilot supports the openai subscription provider only");
  }
  if (
    (config.model !== null && (typeof config.model !== "string" || !config.model.trim())) ||
    (requireModel && config.model === null)
  ) {
    throw new SetupError("Choose a model locally");
  }
  if (
    config.maxOutputTokensPerRequest !== null &&
    (
      !Number.isInteger(config.maxOutputTokensPerRequest) ||
      config.maxOutputTokensPerRequest < 1 ||
      config.maxOutputTokensPerRequest > 8192
    ) ||
    (config.maxModelRequests !== null || requireModel) &&
    (
      !Number.isInteger(config.maxModelRequests) ||
      config.maxModelRequests < 1 ||
      config.maxModelRequests > 12
    )
  ) {
    throw new SetupError("Choose output tokens per request (1..8192, or null for no hard cap) and model requests (1..12)");
  }

  return config;
}

export function requireSubscription(provider, credential) {
  if (!provider?.auth.oauth?.isSubscription || credential?.type !== "oauth") {
    throw new SetupError("Subscription sign-in is required. No API-key fallback is permitted");
  }
}

export function resolveLiveModel(runtime, config) {
  if (config.provider === "openai" && config.model === "gpt-6.1-terra" && !runtime.getModel(config.provider, config.model)) {
    runtime.registerProvider("openai", {
      models: [...runtime.getModels("openai"), {
        id: "gpt-6.1-terra",
        name: "GPT-6.1 Terra",
        api: "openai-responses",
        reasoning: true,
        input: ["text", "image"],
        cost: { input: 1, output: 5, cacheRead: 0.05, cacheWrite: 1.25 },
        contextWindow: 1050000,
        maxTokens: 128000,
      }],
    });
  }
  const model = runtime.getModel(config.provider, config.model);
  if (!model || model.api !== "openai-responses" || config.provider !== "openai") {
    throw new SetupError("Choose an available openai subscription Responses model from the listed models; the selected model was not substituted");
  }
  return model;
}

export function liveReadiness(runtime, config, signedIn) {
  try {
    validateConfig(config);
    resolveLiveModel(runtime, config);
    if (!signedIn) {
      throw new SetupError("Run setup.mjs login to sign Pi in separately. Do not paste credentials into chat.");
    }
    if (config.maxOutputTokensPerRequest !== null) {
      throw new SetupError("ChatGPT subscription requests omit max_output_tokens; the configured hard output cap cannot be enforced");
    }
    return { liveExecutionEnabled: true, liveBlocker: null };
  } catch (error) {
    if (!(error instanceof SetupError)) throw error;
    return { liveExecutionEnabled: false, liveBlocker: error.message };
  }
}

export async function prepareLoginOptions(agentDir) {
  const settings = SettingsManager.create(agentDir, agentDir, { projectTrusted: false });
  const deviceId = settings.getOrCreateDeviceId();
  await settings.flush();
  if (settings.drainErrors().length) {
    throw new SetupError("Cannot read or save Pi's installation ID. Check ~/.pi/agent/settings.json is valid and writable");
  }
  if (
    typeof deviceId !== "string" ||
    !/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(deviceId)
  ) {
    throw new SetupError("Pi's saved installation ID is invalid. Check deviceId in ~/.pi/agent/settings.json");
  }
  return { getDeviceId: () => deviceId, agentName: "Mapbox TUI harness" };
}

export function setupErrorMessage(error) {
  for (let depth = 0; error && depth < 4; depth++, error = error.cause) {
    if (error instanceof SetupError) {
      return error.message;
    }
    if (error.message?.includes("requires a device ID (UUID)")) {
      return "Sign-in requires a saved Pi installation ID";
    }
    if (error.code === "EADDRINUSE" || error.message?.startsWith("Port 1455 is in use")) {
      return "Sign-in callback port 1455 is busy. Finish or cancel the other Pi/Codex login, then retry";
    }
    if (error.message === "Login cancelled" || error.name === "AbortError") {
      return "Sign-in cancelled";
    }
    if (["EACCES", "EPERM"].includes(error.code)) {
      return "Pi setup cannot access its local files. Check their permissions";
    }
    if (["ENOTFOUND", "ECONNREFUSED", "ETIMEDOUT", "ECONNRESET"].includes(error.code)) {
      return `Sign-in connection failed (${error.code}). Check your connection and retry`;
    }
    const status = error.message?.match(/^OpenAI OAuth token request failed \((\d{3})\)/)?.[1];
    if (status) {
      return `OpenAI sign-in token exchange failed (HTTP ${status}). Start a fresh login`;
    }
    if (error.message?.includes("OAuth grant did not include chatgpt.tokens.use.direct")) {
      return "ChatGPT plan usage was not authorised. Retry sign-in and review the requested permission";
    }
  }
  return "Subscription setup failed. Check config.local.json and retry; private provider responses are not printed";
}

async function main() {
  const action = process.argv[2] || "status";
  if (!["status", "login"].includes(action)) {
    throw new SetupError("Use status or login");
  }

  const file = new URL("./config.local.json", import.meta.url);
  const config = validateConfig(JSON.parse(await readFile(file, "utf8")), false);
  const authPath = join(homedir(), ".pi", "agent", "auth.json");
  const runtime = await ModelRuntime.create({
    authPath,
    modelsPath: null,
    allowModelNetwork: false,
    refreshOnCreate: false,
  });

  const provider = runtime.getProvider(config.provider);
  if (!provider?.auth.oauth?.isSubscription) {
    throw new SetupError("Selected provider has no supported subscription sign-in");
  }

  if (action === "login") {
    if (!process.stdin.isTTY) {
      throw new SetupError("Run login in your own interactive terminal");
    }

    const loginOptions = await prepareLoginOptions(dirname(authPath));
    const terminal = createInterface({ input: process.stdin, output: process.stdout });
    const abort = new AbortController();
    const cancel = () => abort.abort();
    process.once("SIGINT", cancel);

    try {
      await runtime.login(
        config.provider,
        "oauth",
        {
          signal: abort.signal,
          async prompt(prompt) {
            if (prompt.type === "secret") {
              throw new SetupError("This setup accepts subscription sign-in only");
            }
            if (prompt.type === "select") {
              console.log(prompt.options.map(option => `${option.id}: ${option.label}`).join("\n"));
            }

            return terminal.question(prompt.message + " ", {
              signal: prompt.signal || abort.signal,
            });
          },
          notify(event) {
            if (event.type === "auth_url") {
              console.log("Open in your browser:\n" + event.url);
            } else if (event.type === "device_code") {
              console.log(event.verificationUri + " Code: " + event.userCode);
            } else {
              console.log(event.message);
            }
          },
        },
        loginOptions,
      );
    } finally {
      process.removeListener("SIGINT", cancel);
      terminal.close();
    }
  }

  const credential = readStoredCredential(config.provider, authPath);
  let signedIn = false;
  try {
    requireSubscription(provider, credential);
    signedIn = true;
  } catch {}

  const readiness = liveReadiness(runtime, config, signedIn);
  console.log(JSON.stringify({
    provider: config.provider,
    model: config.model,
    auth: "subscription",
    signedIn,
    modelCalls: 0,
    ...readiness,
    models: runtime.getModels(config.provider).map(model => model.id),
    next: readiness.liveExecutionEnabled
      ? "Use pi_run.py --image <prepared image ID> --live after reviewing the request/time limits. A null output-token cap uses the provider default."
      : readiness.liveBlocker,
  }, null, 2));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    console.error(`Setup did not complete: ${setupErrorMessage(error)}. No model request was made.`);
    process.exitCode = 1;
  });
}
