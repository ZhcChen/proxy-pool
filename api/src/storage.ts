import { Database } from "bun:sqlite";
import { mkdir, readFile, rename } from "node:fs/promises";
import { existsSync } from "node:fs";
import { join } from "node:path";
import type { Instance, ProxyAuth, State, Subscription } from "./types";

/** 生成随机用户名（8-12 位小写字母+数字） */
function generateRandomUsername(): string {
  const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
  const lenByte = new Uint8Array(1);
  crypto.getRandomValues(lenByte);
  const len = 8 + (lenByte[0] % 5); // 8-12
  let result = "";
  const bytes = new Uint8Array(len);
  crypto.getRandomValues(bytes);
  for (let i = 0; i < len; i++) {
    result += chars[bytes[i] % chars.length];
  }
  return result;
}

/** 生成强密码（24 位，仅包含大小写字母与数字；更适合直接用于代理 URL userinfo） */
function generateStrongPassword(): string {
  const lower = "abcdefghijklmnopqrstuvwxyz";
  const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  const digits = "0123456789";
  const all = lower + upper + digits;
  const len = 24;

  const randIndex = (mod: number): number => {
    const b = new Uint32Array(1);
    crypto.getRandomValues(b);
    return b[0] % mod;
  };

  // 确保至少包含每种类型各一个
  const required = [
    lower[randIndex(lower.length)],
    upper[randIndex(upper.length)],
    digits[randIndex(digits.length)]
  ];

  const bytes = new Uint8Array(len - required.length);
  crypto.getRandomValues(bytes);
  const rest: string[] = [];
  for (let i = 0; i < bytes.length; i++) {
    rest.push(all[bytes[i] % all.length]);
  }

  // 合并并打乱顺序
  const combined = [...required, ...rest];
  for (let i = combined.length - 1; i > 0; i--) {
    const j = randIndex(i + 1);
    [combined[i], combined[j]] = [combined[j], combined[i]];
  }
  return combined.join("");
}

/** 生成新的代理认证凭据 */
export function generateProxyAuth(): ProxyAuth {
  return {
    enabled: false,
    username: generateRandomUsername(),
    password: generateStrongPassword()
  };
}

const DEFAULT_STATE: State = {
  version: 1,
  settings: {
    bindAddress: "127.0.0.1",
    allowLan: false,
    logLevel: "info",
    baseMixedPort: 30001,
    baseControllerPort: 40001,
    maxLogLines: 800,
    healthCheckIntervalSec: 60,
    healthCheckUrl: "http://www.gstatic.com/generate_204",
    exportHost: "",
    proxyAuth: generateProxyAuth()
  },
  subscriptions: [],
  instances: []
};

export class Storage {
  readonly dataDir: string;
  readonly dbPath: string;
  private db: Database | null = null;

  constructor(dataDir: string) {
    this.dataDir = dataDir;
    this.dbPath = join(dataDir, "state.sqlite");
  }

  private openDb(): Database {
    if (this.db) return this.db;
    const db = new Database(this.dbPath);
    db.exec("PRAGMA journal_mode = WAL;");
    db.exec("PRAGMA foreign_keys = ON;");
    db.exec("CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);");
    this.db = db;
    return db;
  }

  async ensure(): Promise<void> {
    await mkdir(this.dataDir, { recursive: true });
    await mkdir(join(this.dataDir, "instances"), { recursive: true });
    this.openDb();
  }

  private normalizeState(maybe: unknown): State {
    const fallback = structuredClone(DEFAULT_STATE);
    if (!maybe || typeof maybe !== "object") return fallback;
    const parsed = maybe as any;
    if (parsed?.version !== 1) return fallback;

    const settings = parsed?.settings ?? {};

    // 处理 proxyAuth：如果旧数据没有或格式不对，生成新的
    let proxyAuth = fallback.settings.proxyAuth;
    if (settings.proxyAuth && typeof settings.proxyAuth === "object") {
      const pa = settings.proxyAuth;
      proxyAuth = {
        enabled: typeof pa.enabled === "boolean" ? pa.enabled : false,
        username: typeof pa.username === "string" && pa.username ? pa.username : generateProxyAuth().username,
        password: typeof pa.password === "string" && pa.password ? pa.password : generateProxyAuth().password
      };
    }

    const mergedSettings = {
      ...fallback.settings,
      bindAddress: typeof settings.bindAddress === "string" && settings.bindAddress ? settings.bindAddress : fallback.settings.bindAddress,
      allowLan: typeof settings.allowLan === "boolean" ? settings.allowLan : fallback.settings.allowLan,
      logLevel: ["silent", "error", "warning", "info", "debug"].includes(settings.logLevel) ? settings.logLevel : fallback.settings.logLevel,
      baseMixedPort: Number.isFinite(settings.baseMixedPort) ? Number(settings.baseMixedPort) : fallback.settings.baseMixedPort,
      baseControllerPort: Number.isFinite(settings.baseControllerPort) ? Number(settings.baseControllerPort) : fallback.settings.baseControllerPort,
      maxLogLines: Number.isFinite(settings.maxLogLines) ? Number(settings.maxLogLines) : fallback.settings.maxLogLines,
      healthCheckIntervalSec:
        Number.isFinite(settings.healthCheckIntervalSec) && Number(settings.healthCheckIntervalSec) >= 0
          ? Number(settings.healthCheckIntervalSec)
          : fallback.settings.healthCheckIntervalSec,
      healthCheckUrl:
        typeof settings.healthCheckUrl === "string" && settings.healthCheckUrl.trim()
          ? settings.healthCheckUrl.trim()
          : fallback.settings.healthCheckUrl,
      exportHost: typeof settings.exportHost === "string" ? settings.exportHost.trim() : fallback.settings.exportHost,
      proxyAuth
    };

    const subscriptions: Subscription[] = Array.isArray(parsed.subscriptions)
      ? parsed.subscriptions.filter((s: unknown) => !!s && typeof s === "object")
      : [];

    const instances: Instance[] = Array.isArray(parsed.instances)
      ? parsed.instances
          .filter((i: unknown) => !!i && typeof i === "object")
          .map((i: any) => ({
            ...i,
            autoSwitch: typeof i.autoSwitch === "boolean" ? i.autoSwitch : false
          }))
      : [];

    return {
      version: 1,
      settings: mergedSettings,
      subscriptions,
      instances
    };
  }

  private tryLoadLegacyStateJson(): Promise<State | null> {
    const legacyPath = join(this.dataDir, "state.json");
    if (!existsSync(legacyPath)) return Promise.resolve(null);
    return readFile(legacyPath, "utf8")
      .then((raw) => this.normalizeState(JSON.parse(raw)))
      .catch(() => null);
  }

  async loadState(): Promise<State> {
    await this.ensure();
    const db = this.openDb();

    const row = db.query("SELECT value FROM kv WHERE key = ?").get("state") as { value: string } | null;
    if (row?.value) {
      try {
        return this.normalizeState(JSON.parse(row.value));
      } catch {
        // fallthrough
      }
    }

    const legacy = await this.tryLoadLegacyStateJson();
    const initial = legacy ?? structuredClone(DEFAULT_STATE);
    db.query("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)").run("state", JSON.stringify(initial));

    if (legacy) {
      try {
        await rename(join(this.dataDir, "state.json"), join(this.dataDir, "state.json.bak"));
      } catch {
        // ignore
      }
    }

    return initial;
  }

  async saveState(state: State): Promise<void> {
    await this.ensure();
    const db = this.openDb();
    db.query("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)").run("state", JSON.stringify(state));
  }

  getKv(key: string): string | null {
    const db = this.openDb();
    const row = db.query("SELECT value FROM kv WHERE key = ?").get(key) as { value: string } | null;
    return row?.value ?? null;
  }

  setKv(key: string, value: string): void {
    const db = this.openDb();
    db.query("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)").run(key, value);
  }

  deleteKv(key: string): void {
    const db = this.openDb();
    db.query("DELETE FROM kv WHERE key = ?").run(key);
  }

  getJson<T>(key: string): T | null {
    const raw = this.getKv(key);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as T;
    } catch {
      return null;
    }
  }

  setJson(key: string, value: unknown): void {
    this.setKv(key, JSON.stringify(value));
  }

  deleteJson(key: string): void {
    this.deleteKv(key);
  }
}
