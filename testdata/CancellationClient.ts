// Exercises caller cancellation through generated JSON routes and response-body reads.
import { createApiClient } from "./api.gen";

declare const process: { argv: string[] };
const base = process.argv[2];

async function main() {
  let headersSeen: () => void = () => { throw new Error("unexpected request"); };
  const client = createApiClient(async (url, init) => {
    const response = await fetch(base + url, init);
    headersSeen();
    return response;
  });
  for (const invoke of [
    (signal: AbortSignal) => client.read(signal),
    (signal: AbortSignal) => client.write({ kind: "request" }, signal),
    (signal: AbortSignal) => client.query({ limit: 3 }, signal),
    (signal: AbortSignal) => client.queryHeaders({ limit: 3 }, { "X-Test": "expected" }, signal),
  ]) {
    const controller = new AbortController();
    const ready = new Promise<void>((resolve) => { headersSeen = resolve; });
    const pending = invoke(controller.signal);
    await ready;
    controller.abort();
    try {
      await pending;
      throw new Error("request resolved after cancellation");
    } catch (error) {
      if (!(error instanceof Error) || error.name !== "AbortError") throw error;
    }
    // The server must observe cancellation while this process and all gates
    // remain alive, rather than having process teardown close the connection.
    const stopped = await fetch(base + "/canceled", { signal: AbortSignal.timeout(2000) });
    if ((await stopped.text()) !== "canceled") throw new Error("server did not observe cancellation");
  }
  let calls = 0;
  const canceled = new AbortController();
  canceled.abort();
  const local = createApiClient(async () => {
    calls++;
    return Response.json({ kind: "unexpected" });
  });
  try {
    await local.read(canceled.signal);
    throw new Error("already canceled request resolved");
  } catch (error) {
    if (!(error instanceof Error) || error.name !== "AbortError") throw error;
  }
  if (calls !== 0) throw new Error("already canceled request reached transport");

  const timeout = AbortSignal.timeout;
  try {
    for (const caller of [undefined, new AbortController().signal]) {
      const deadline = new AbortController();
      AbortSignal.timeout = (milliseconds: number) => {
        if (milliseconds !== 60_000) throw new Error("request deadline changed");
        return deadline.signal;
      };
      const deadlineClient = createApiClient((_url, init) => new Promise((_resolve, reject) => {
        const signal = init?.signal;
        if (!signal) throw new Error("request has no deadline signal");
        signal.addEventListener("abort", () => reject(signal.reason), { once: true });
      }));
      const pending = deadlineClient.read(caller);
      const expired = new DOMException("Request deadline expired", "TimeoutError");
      deadline.abort(expired);
      try {
        await pending;
        throw new Error("deadline did not abort request");
      } catch (error) {
        if (error !== expired) throw error;
      }
    }
  } finally {
    AbortSignal.timeout = timeout;
  }
  console.log("cancellation verified");
}
const watchdog = setTimeout(() => { throw new Error("Cancellation fixture did not complete"); }, 10_000);
void main().finally(() => clearTimeout(watchdog));
