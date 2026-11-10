import type { testMetaIdentity } from "./types.gen";
export interface testCustomMeta { kind: string; identity: testMetaIdentity; [key: string]: unknown }
export function checkMeta(value: unknown, path: string): testCustomMeta {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new TypeError(path);
  const obj = value as Record<string, unknown>;
  if (typeof obj.kind !== "string") throw new TypeError(path);
  if (typeof obj.identity !== "object" || obj.identity === null || Array.isArray(obj.identity) || typeof (obj.identity as Record<string, unknown>).name !== "string") throw new TypeError(path);
  return obj as testCustomMeta;
}
