import { createInterface } from "node:readline";
import { runNativeScriptedLoop } from "./native.mjs";
import { SetupError } from "./setup.mjs";

const lines = createInterface({ input: process.stdin });
const iterator = lines[Symbol.asyncIterator]();
const emit = value => process.stdout.write(JSON.stringify(value) + "\n");
let live = false;
try {
  const first = await iterator.next();
  if (first.done) throw new Error("Missing startup data");
  const start = JSON.parse(first.value);
  live = start.mode === "live";
  if (!process.permission || process.permission.has("child") || process.permission.has("net") !== live) {
    throw new Error("Unexpected Node permissions for this normal-tool session");
  }
  const run = live ? (await import("./native-live.mjs")).runNativeLiveLoop : runNativeScriptedLoop;
  await run(start, async request => {
    emit(request);
    const next = await iterator.next();
    if (next.done) throw new Error("The runner disconnected");
    const reply = JSON.parse(next.value);
    if (reply.id !== request.id) throw new Error("Runner reply ID mismatch");
    return reply.result;
  }, emit);
} catch (error) {
  process.stderr.write(live ? "The live normal-tool session stopped; see its safe outcome\n" : String(error.stack || error) + "\n");
  emit({ type: "failed", outcome: live ? error.outcome ?? "blocked" : "blocked",
    reason: live && (error.outcome || error instanceof SetupError) ? error.message : "The normal-tool session stopped; inspect its local log" });
  process.exitCode = 1;
} finally {
  lines.close();
}
