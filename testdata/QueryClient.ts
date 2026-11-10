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
  await client.reuse(query, {limit:null, unread:null, text:null, ids:null, extra:"body"});
  if (await client.rootList(null) !== null || await client.rootMap(null) !== null || await client.rootArray() !== null) throw new Error("nullable root");
  await client.queryOnlyEmpty({}); await client.queryOnlyValue({since:"x"});
  if (await client.rootBytesHeaders({"X-Test":"expected"}) !== "AAH/") throw new Error("header byte response");
  if (await client.rootBytes("AAH/") !== "AAH/" || JSON.stringify(await client.rootNestedBytes(["AAH/", null])) !== '["AAH/",null]') throw new Error("base64 root");
  if (JSON.stringify(await client.rootBytesMap({x:"AAH/", empty:null})) !== '{"empty":null,"x":"AAH/"}') throw new Error("base64 map root");
  if ((await client.rootList([]))?.length !== 0 || Object.keys((await client.rootMap({})) ?? {x:1}).length !== 0) throw new Error("empty root");
  client.events(query, {onMessage() {}, onError(error) { throw error; }});
  client.namedEvents(query, {onMessage() {}, onError(error) { throw error; }});
  for (const url of urls) {
    const parsed = new URL(url, base);
    if (parsed.searchParams.size !== 4 || parsed.searchParams.get("limit") !== "0" || parsed.searchParams.get("unread_only") !== "false" || parsed.searchParams.get("q") !== "a +&雪" || parsed.searchParams.get("ids") !== "a,b") throw new Error("SSE query encoding failed");
  }
  if (urls.length !== 2) throw new Error("SSE routes missing");
}
void main();
