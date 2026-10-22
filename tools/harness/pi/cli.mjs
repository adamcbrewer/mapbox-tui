import { createInterface } from "node:readline";
import { runScriptedLoop } from "./loop.mjs";
import { runLiveLoop } from "./live.mjs";

const lines = createInterface({ input: process.stdin });
const iterator = lines[Symbol.asyncIterator]();
const emit = value => process.stdout.write(JSON.stringify(value) + "\n");

try {
  const first = await iterator.next();
  if (first.done) {
    throw new Error("Missing startup context");
  }

  const start = JSON.parse(first.value);
  if (!process.permission || process.permission.has("child") ||
      process.permission.has("net") !== (start.mode === "live")) {
    throw new Error("Unexpected Node permissions");
  }
  const run = start.mode === "live" ? runLiveLoop : runScriptedLoop;
  await run(start, async request => {
    emit(request);
    const reply = await iterator.next();
    if (reply.done) {
      throw new Error("Runner disconnected");
    }

    const message = JSON.parse(reply.value);
    if (message.id !== request.id) {
      throw new Error("Runner reply ID mismatch");
    }

    return message.result;
  }, emit);
} catch (error) {
  const { SetupError, setupErrorMessage } = await import("./setup.mjs");
  emit({ type: "failed", outcome: error instanceof SetupError && error.outcome ? error.outcome : "blocked",
    reason: setupErrorMessage(error) });
  process.stderr.write("Pi stopped. See the safe failure outcome in events.jsonl.\n");
  process.exitCode = 1;
} finally {
  lines.close();
}
