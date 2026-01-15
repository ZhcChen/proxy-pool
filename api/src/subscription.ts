import YAML from "yaml";
import type { MihomoProxy } from "./types";

function looksLikeBase64(text: string): boolean {
  const trimmed = text.trim();
  if (trimmed.length < 64) return false;
  if (trimmed.includes("\n")) return false;
  if (!/^[A-Za-z0-9+/=]+$/.test(trimmed)) return false;
  if (trimmed.length % 4 !== 0) return false;
  return true;
}

function tryDecodeBase64ToUtf8(text: string): string | null {
  try {
    const decoded = Buffer.from(text.trim(), "base64").toString("utf8");
    if (!decoded) return null;
    // 粗略判断是否为 YAML/Clash 配置
    if (decoded.includes("proxies:") || decoded.includes("proxy-groups:")) return decoded;
    return null;
  } catch {
    return null;
  }
}

export function parseSubscriptionYaml(input: string): { proxies: MihomoProxy[] } {
  const raw = input.trim();
  const maybeDecoded = looksLikeBase64(raw) ? tryDecodeBase64ToUtf8(raw) : null;
  const yamlText = maybeDecoded ?? raw;

  const doc = YAML.parse(yamlText) as unknown;
  if (!doc || typeof doc !== "object") {
    throw new Error("订阅内容不是有效的 YAML 对象");
  }

  const proxies = (doc as any).proxies as unknown;
  if (!Array.isArray(proxies)) {
    throw new Error("订阅中未找到 proxies 列表（暂不支持 proxy-providers 自动展开）");
  }

  const cleaned: MihomoProxy[] = [];
  for (const item of proxies) {
    if (!item || typeof item !== "object") continue;
    const name = (item as any).name;
    if (typeof name !== "string" || !name.trim()) continue;
    cleaned.push(item as MihomoProxy);
  }

  if (cleaned.length === 0) throw new Error("订阅 proxies 为空或无法解析节点 name");
  return { proxies: cleaned };
}

