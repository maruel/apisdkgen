// Exercises typed URL queries independently of JSON bodies and SSE URLs.
import { createApiClient } from "./api.gen";

declare const process: { argv: string[] };
const base = process.argv[2];
const urls: string[] = [];
class FixtureEventSource {
  constructor(url: string) { urls.push(url); }
  addEventListener() {}
}
Object.assign(globalThis, { EventSource: FixtureEventSource });
async function main() {
  const client = createApiClient((url, init) => fetch(base + url, init));
  const query = { limit: 0, unread: false, text: "a +&雪", ids: "a,b", extra: "ignored" };
  if ((await client.list("full", query)).kind !== "listed") throw new Error("query response failed");
  await client.list("blank", {...query, text: ""});
  await client.list("empty", { extra: "ignored" });
  await client.update(query, {kind: "request"}, {"X-Test": "expected"});
  client.events(query, {onMessage() {}, onError(error) { throw error; }});
  client.namedEvents(query, {onMessage() {}, onError(error) { throw error; }});
  for (const url of urls) {
    const parsed = new URL(url, base);
    if (parsed.searchParams.size !== 4 || parsed.searchParams.get("limit") !== "0" || parsed.searchParams.get("unread_only") !== "false" || parsed.searchParams.get("q") !== "a +&雪" || parsed.searchParams.get("ids") !== "a,b") throw new Error("SSE query encoding failed");
  }
  if (urls.length !== 2) throw new Error("SSE routes missing");
}
void main();
