import { join } from "node:path";
import { ModelRuntime, readStoredCredential } from "@earendil-works/pi-coding-agent";
import { boundedStream } from "./live.mjs";
import { nativeNames, runNativeLoop } from "./native.mjs";
import { requireSubscription, resolveLiveModel, validateConfig } from "./setup.mjs";

export async function runNativeLiveLoop(start, rpc, emit) {
  const config = validateConfig(start.config);
  const authPath = join(start.authDir, "auth.json");
  const runtime = await ModelRuntime.create({ authPath, modelsPath: null,
    allowModelNetwork: false, refreshOnCreate: false });
  const checkSubscription = () => requireSubscription(runtime.getProvider(config.provider),
    readStoredCredential(config.provider, authPath));
  checkSubscription();
  const model = resolveLiveModel(runtime, config);
  const requests = { count: 0 };
  const reserve = request => rpc({ type: "model_reserve", id: `model-${request}`,
    request, provider: config.provider, model: config.model });
  return runNativeLoop(start, rpc, emit, { runtime, model, requests,
    streamFunction: boundedStream(runtime, model, config, requests, emit, checkSubscription, 120000, reserve, nativeNames) });
}
