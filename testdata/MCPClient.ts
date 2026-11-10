// Exercises the generated generic JSON-RPC route with Go-encoded request fields.
import { createApiClient } from "./api.gen";
import type { JSONRPCRequest } from "./types.gen";
declare function require(name: string): { readFileSync(path: string, encoding: string): string };
declare const process: { argv: string[] };
async function main() {
  const client = createApiClient((url, init) => fetch(process.argv[2] + url, init));
  const req = JSON.parse(require("fs").readFileSync(process.argv[3], "utf8")) as JSONRPCRequest;
  const response = await client.mcp(req, { "Mcp-Method": "server/discover", "Mcp-Protocol-Version": "2026-07-28" });
  if ((response.result as { instructions: string }).instructions !== "ready") throw new Error("request failed");
}
void main();
