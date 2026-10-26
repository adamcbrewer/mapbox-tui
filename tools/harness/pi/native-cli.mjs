import { createInterface } from "node:readline";
import { runNativeScriptedLoop } from "./native.mjs";

const lines = createInterface({ input: process.stdin });
const iterator = lines[Symbol.asyncIterator]();
const emit = value => process.stdout.write(JSON.stringify(value) + "\n");
try {
  if (!process.permission || process.permission.has("child") || process.permission.has("net")) {
    throw new Error("The prepared normal-tool check needs Node without child-process or network access");
  }
  const first = await iterator.next();
  if (first.done) throw new Error("Missing startup data");
  const start = JSON.parse(first.value);
  await runNativeScriptedLoop(start, async request => {
    emit(request);
    const next = await iterator.next();
    if (next.done) throw new Error("The runner disconnected");
    const reply = JSON.parse(next.value);
    if (reply.id !== request.id) throw new Error("Runner reply ID mismatch");
    return reply.result;
  }, emit);
} catch (error) {
  process.stderr.write(String(error.stack || error) + "\n");
  emit({ type: "failed", outcome: "blocked", reason: "The normal-tool session stopped; inspect its local log" });
  process.exitCode = 1;
} finally {
  lines.close();
}
