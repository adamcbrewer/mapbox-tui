import { join } from "node:path";
import { ModelRuntime, readStoredCredential } from "@earendil-works/pi-coding-agent";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { runToolLoop, schemas } from "./loop.mjs";
import { requireSubscription, resolveLiveModel, SetupError, validateConfig } from "./setup.mjs";

function stopped(outcome, reason) {
  return Object.assign(new SetupError(reason), { outcome });
}

function safeModelFailure(message) {
  const diagnostic = message.errorMessage ?? "";
  if (/subscription_sharing_usage_limit_exceeded|usage.limit|quota|rate.limit|429/i.test(diagnostic)) {
    return "The subscription usage or rate limit stopped the model request";
  }
  if (/401|403|unauthori[sz]ed|authentication/i.test(diagnostic)) {
    return "The provider rejected subscription authentication or access";
  }
  if (/model.*(not found|not available|does not exist)|404/i.test(diagnostic)) {
    return "The selected model is unavailable to this subscription";
  }
  return "The model request failed; no provider response or credentials were copied to diagnostics";
}

export function boundedStream(runtime, model, config, requests, emit, checkSubscription, requestTimeoutMs = 120000) {
  return (requestedModel, context, options) => {
    const output = createAssistantMessageEventStream();
    (async () => {
      let timer;
      let signal;
      let onAbort;
      try {
        if (requestedModel.provider !== model.provider || requestedModel.id !== model.id) {
          throw stopped("blocked", "The model identity changed during the run");
        }
        if (requests.count >= config.maxModelRequests) {
          throw stopped("budget_exhausted", "Model-request budget reached; no further request was sent");
        }
        checkSubscription();
        const controller = new AbortController();
        signal = options?.signal ? AbortSignal.any([options.signal, controller.signal]) : controller.signal;
        signal.throwIfAborted();
        const interrupted = new Promise((_, reject) => {
          onAbort = () => reject(signal.reason);
          if (signal.aborted) onAbort();
          else signal.addEventListener("abort", onAbort, { once: true });
        });
        timer = setTimeout(() => controller.abort(stopped("timeout", "The model request reached its full-response time limit")),
          requestTimeoutMs);
        const request = ++requests.count;
        let providerUsage = null;
        emit({ type: "model_request", request, provider: model.provider, model: model.id,
          maxOutputTokens: config.maxOutputTokensPerRequest, reasoning: "low", maxRetries: 0 });
        const stream = runtime.streamSimple(model, context, {
          signal,
          ...(config.maxOutputTokensPerRequest === null ? {} : { maxTokens: config.maxOutputTokensPerRequest }),
          reasoning: "low",
          maxRetries: 0,
          timeoutMs: requestTimeoutMs,
          transport: "sse",
          cacheRetention: "none",
          onProviderStreamEvent(event) {
            if (!["response.completed", "response.incomplete", "response.failed"].includes(event.type)) return;
            const usage = event.response?.usage;
            if (usage) {
              providerUsage = { inputTokens: usage.input_tokens, outputTokens: usage.output_tokens,
                cachedInputTokens: usage.input_tokens_details?.cached_tokens ?? 0,
                reasoningTokens: usage.output_tokens_details?.reasoning_tokens ?? null,
                totalTokens: usage.total_tokens };
              emit({ type: "model_usage", request, completion: event.type, usage: providerUsage });
            }
          },
          fetch(input, init) {
            emit({ type: "model_dispatch", request });
            return globalThis.fetch(input, init);
          },
          onPayload(payload) {
            const cap = config.maxOutputTokensPerRequest;
            if (cap !== null && payload.max_output_tokens === undefined) {
              requests.failure = stopped("blocked", "The subscription transport omitted the configured output-token cap; no model request was sent");
              emit({ type: "model_payload_rejected", request, reason: requests.failure.message });
              throw requests.failure;
            }
            const invalidCap = cap === null ? payload.max_output_tokens !== undefined :
              !Number.isInteger(payload.max_output_tokens) || payload.max_output_tokens < 1 || payload.max_output_tokens > cap;
            if (payload.model !== config.model || invalidCap ||
                JSON.stringify(payload.tools?.map(tool => tool.name).sort()) !==
                  JSON.stringify(schemas.map(tool => tool.name).sort())) {
              requests.failure = stopped("blocked", "The provider payload exceeded the approved model, token or tool scope");
              throw requests.failure;
            }
            emit({ type: "model_payload", request, model: payload.model,
              maxOutputTokens: payload.max_output_tokens ?? null, tools: payload.tools.map(tool => tool.name) });
          },
        });
        const iterator = stream[Symbol.asyncIterator]();
        while (true) {
          const { value: event, done } = await Promise.race([iterator.next(), interrupted]);
          if (done) break;
          if (event.type === "done" || event.type === "error") {
            const message = event.message ?? event.error;
            emit({ type: "model_response", request, stopReason: message.stopReason, usage: message.usage, providerUsage });
            if (message.stopReason === "length") {
              throw stopped("budget_exhausted", "Output-token limit reached; partial output was not executed");
            }
            if (["error", "aborted"].includes(message.stopReason)) {
              throw requests.failure ?? stopped("blocked", safeModelFailure(message));
            }
          }
          output.push(event);
        }
        output.end();
      } catch (error) {
        requests.failure = error instanceof SetupError ? error : stopped("blocked", "Model transport could not complete");
        const message = { role: "assistant", provider: model.provider, model: model.id, api: model.api,
          content: [], stopReason: "error", errorMessage: requests.failure.message, timestamp: Date.now(),
          usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
            cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
        output.push({ type: "error", reason: "error", error: message });
        output.end();
      } finally {
        clearTimeout(timer);
        if (onAbort) signal.removeEventListener("abort", onAbort);
      }
    })();
    return output;
  };
}

export async function runLiveLoop(start, rpc, emit) {
  const config = validateConfig(start.config);
  const authPath = join(start.authDir, "auth.json");
  const runtime = await ModelRuntime.create({ authPath, modelsPath: null,
    allowModelNetwork: false, refreshOnCreate: false });
  const checkSubscription = () => requireSubscription(runtime.getProvider(config.provider),
    readStoredCredential(config.provider, authPath));
  checkSubscription();
  const model = resolveLiveModel(runtime, config);
  const requests = { count: 0 };
  return runToolLoop(start, rpc, emit, { runtime, model, config, requests, thinkingLevel: "low",
    streamFunction: boundedStream(runtime, model, config, requests, emit, checkSubscription) });
}
