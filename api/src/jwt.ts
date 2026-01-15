import { createHmac, timingSafeEqual } from "node:crypto";

export type JwtPayload = {
  sub: string;
  iat: number;
  exp: number;
  iss?: string;
  jti?: string;
};

function base64UrlEncode(input: Uint8Array): string {
  return Buffer.from(input)
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

function base64UrlDecode(input: string): Buffer {
  let s = String(input || "");
  s = s.replace(/-/g, "+").replace(/_/g, "/");
  const pad = s.length % 4;
  if (pad) s += "=".repeat(4 - pad);
  return Buffer.from(s, "base64");
}

function jsonToB64Url(obj: unknown): string {
  return base64UrlEncode(Buffer.from(JSON.stringify(obj), "utf8"));
}

function safeJsonParseB64Url<T>(segment: string): T | null {
  try {
    const raw = base64UrlDecode(segment).toString("utf8");
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

type JwtHeader = { alg: string; typ?: string };

export function signJwtHs256(payload: JwtPayload, secret: string): string {
  const header: JwtHeader = { alg: "HS256", typ: "JWT" };
  const headerSeg = jsonToB64Url(header);
  const payloadSeg = jsonToB64Url(payload);
  const data = `${headerSeg}.${payloadSeg}`;
  const sig = createHmac("sha256", secret).update(data).digest();
  const sigSeg = base64UrlEncode(sig);
  return `${data}.${sigSeg}`;
}

export function verifyJwtHs256(token: string, secret: string): JwtPayload | null {
  const parts = String(token || "").split(".");
  if (parts.length !== 3) return null;
  const [headerSeg, payloadSeg, sigSeg] = parts;

  const header = safeJsonParseB64Url<JwtHeader>(headerSeg);
  if (!header || header.alg !== "HS256") return null;

  const payload = safeJsonParseB64Url<JwtPayload>(payloadSeg);
  if (!payload || typeof payload.sub !== "string") return null;
  if (typeof payload.exp !== "number" || !Number.isFinite(payload.exp)) return null;

  const data = `${headerSeg}.${payloadSeg}`;
  const expected = createHmac("sha256", secret).update(data).digest();
  let provided: Buffer;
  try {
    provided = base64UrlDecode(sigSeg);
  } catch {
    return null;
  }
  if (provided.length !== expected.length) return null;
  if (!timingSafeEqual(provided, expected)) return null;

  const now = Math.floor(Date.now() / 1000);
  if (payload.exp <= now) return null;

  return payload;
}

