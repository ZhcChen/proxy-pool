import { join } from "node:path";
import { existsSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import type { Instance, MihomoProxy, State, Subscription } from "./types";
import { Storage, generateProxyAuth } from "./storage";
import { parseSubscriptionYaml } from "./subscription";
import { MihomoManager, type HealthStatus } from "./mihomo";
import { MihomoInstaller } from "./mihomoInstaller";
import { findNextFreePortAvoiding, isPortFree, nowIso, resolveUnder } from "./utils";
import { signJwtHs256, verifyJwtHs256 } from "./jwt";

const REPO_ROOT = join(import.meta.dir, "../..");
const DATA_DIR = process.env.DATA_DIR ?? join(REPO_ROOT, "data");
const WEB_DIR = process.env.WEB_DIR ?? join(REPO_ROOT, "web", "public");

const storage = new Storage(DATA_DIR);
const mihomo = new MihomoManager(DATA_DIR);
const installer = new MihomoInstaller(DATA_DIR, storage, process.env.MIHOMO_REPO ?? "MetaCubeX/mihomo");

let state: State = await storage.loadState();

const AUTH_TOKEN_KEY = "mihomo-pool-token";
const JWT_TTL_SEC = 30 * 24 * 60 * 60;

const PROXY_HEALTH_KEY_PREFIX = "proxy_health:";
function loadProxyHealth(subscriptionId: string): Record<string, HealthStatus> {
  return storage.getJson<Record<string, HealthStatus>>(`${PROXY_HEALTH_KEY_PREFIX}${subscriptionId}`) ?? {};
}
function saveProxyHealth(subscriptionId: string, value: Record<string, HealthStatus>): void {
  storage.setJson(`${PROXY_HEALTH_KEY_PREFIX}${subscriptionId}`, value);
}

const AUTH_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
function randomString(length: number): string {
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  let out = "";
  for (let i = 0; i < bytes.length; i++) out += AUTH_ALPHABET[bytes[i] % AUTH_ALPHABET.length];
  return out;
}

type AdminCredentials = { username: string; password: string; createdAt: string };
function loadOrCreateAdminCredentials(): AdminCredentials {
  const existing = storage.getJson<AdminCredentials>("admin_credentials");
  if (existing?.username && typeof existing.username === "string" && existing?.password && typeof existing.password === "string") {
    return existing;
  }
  const created = { username: randomString(12), password: randomString(20), createdAt: nowIso() };
  storage.setJson("admin_credentials", created);
  return created;
}

const ADMIN = loadOrCreateAdminCredentials();

function loadOrCreateJwtSecret(): string {
  const existing = storage.getKv("jwt_secret");
  if (existing && typeof existing === "string" && existing.length >= 32) return existing;
  const created = randomString(64);
  storage.setKv("jwt_secret", created);
  return created;
}

const JWT_SECRET = loadOrCreateJwtSecret();

function getBearerToken(req: Request): string | null {
  const header = req.headers.get("authorization") ?? "";
  const m = header.match(/^Bearer\s+(.+)$/i);
  return m?.[1]?.trim() ? m[1].trim() : null;
}

function isAuthorized(req: Request): boolean {
  const token = getBearerToken(req);
  if (!token) return false;
  const payload = verifyJwtHs256(token, JWT_SECRET);
  if (!payload) return false;
  if (payload.sub !== ADMIN.username) return false;
  return true;
}

function json(data: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(data, null, 2), {
    ...init,
    headers: {
      "content-type": "application/json; charset=utf-8",
      ...(init.headers ?? {})
    }
  });
}

function badRequest(message: string, details?: unknown): Response {
  return json({ ok: false, error: message, details }, { status: 400 });
}

function unauthorized(): Response {
  return json({ ok: false, error: "unauthorized" }, { status: 401 });
}

function notFound(): Response {
  return json({ ok: false, error: "not found" }, { status: 404 });
}

function withRuntime(i: Instance) {
  const rt = mihomo.getRuntimeStatus(i.id);
  const health = mihomo.getHealthStatus(i.id);
  return { ...i, runtime: rt, health };
}

function getSubscriptionProxiesForInstance(i: Instance): MihomoProxy[] {
  const sub = state.subscriptions.find((s) => s.id === i.subscriptionId);
  if (sub?.proxies?.length) return sub.proxies;
  return [i.proxy];
}

function getSubscriptionForInstance(i: Instance): Subscription | null {
  return state.subscriptions.find((s) => s.id === i.subscriptionId) ?? null;
}

async function checkAndSaveProxyHealth(sub: Subscription, proxyName: string, binPath: string): Promise<HealthStatus> {
  try {
    const res = await mihomo.checkSubscriptionProxyDelay(sub.id, sub.updatedAt, sub.proxies, proxyName, state.settings, binPath);
    const current = loadProxyHealth(sub.id);
    current[proxyName] = res;
    saveProxyHealth(sub.id, current);
    return res;
  } catch (e) {
    const res: HealthStatus = {
      ok: false,
      checkedAt: nowIso(),
      latencyMs: null,
      error: (e as Error).message,
      target: state.settings.healthCheckUrl,
      proxyName
    };
    const current = loadProxyHealth(sub.id);
    current[proxyName] = res;
    saveProxyHealth(sub.id, current);
    return res;
  }
}

async function startInstanceWithPreflight(instance: Instance): Promise<void> {
  const sub = getSubscriptionForInstance(instance);
  if (!sub) throw new Error("实例所属订阅不存在（可能已删除），无法启动");

  const binPath = getInstalledMihomoPath();

  const primary = await checkAndSaveProxyHealth(sub, instance.proxyName, binPath);
  if (primary.ok) {
    await mihomo.start(instance, state.settings, binPath, sub.proxies);
    return;
  }

  if (!instance.autoSwitch) {
    throw new Error(`节点不可用，启动已取消：${primary.error || "检测失败"}`);
  }

  const health = loadProxyHealth(sub.id);
  const candidates = sub.proxies
    .map((p) => p.name)
    .filter((name) => name && name !== instance.proxyName)
    .sort((a, b) => {
      const ha = health[a];
      const hb = health[b];
      const ga = ha?.ok ? 0 : ha ? 2 : 1;
      const gb = hb?.ok ? 0 : hb ? 2 : 1;
      if (ga !== gb) return ga - gb;
      const la = typeof ha?.latencyMs === "number" ? ha.latencyMs : Number.POSITIVE_INFINITY;
      const lb = typeof hb?.latencyMs === "number" ? hb.latencyMs : Number.POSITIVE_INFINITY;
      if (la !== lb) return la - lb;
      return a.localeCompare(b, "zh-CN");
    });

  let preferred: string | null = null;
  for (const name of candidates) {
    const res = await checkAndSaveProxyHealth(sub, name, binPath);
    if (res.ok) {
      preferred = name;
      break;
    }
  }

  if (!preferred) {
    throw new Error(`订阅内没有可用节点，启动已取消：${primary.error || "检测失败"}`);
  }

  await mihomo.start(instance, state.settings, binPath, sub.proxies, preferred);
}

function collectReservedPorts(): Set<number> {
  const reserved = new Set<number>();
  for (const i of state.instances) {
    reserved.add(i.mixedPort);
    reserved.add(i.controllerPort);
  }
  return reserved;
}

function isAllSubscriptionValue(value: string): boolean {
  const v = String(value || "").trim().toLowerCase();
  return v === "" || v === "all" || v === "__all__";
}

function isAutoProxyValue(value: string): boolean {
  const v = String(value || "").trim().toLowerCase();
  return v === "" || v === "all" || v === "__auto__";
}

type PickedProxy = {
  subscriptionId: string;
  subscription: Subscription;
  proxyName: string;
  proxy: MihomoProxy;
  health: HealthStatus | null;
};

function listUnusedProxyCandidates(scopeSubscriptionId: string): PickedProxy[] {
  const wantAll = isAllSubscriptionValue(scopeSubscriptionId);
  const subs = wantAll ? state.subscriptions : state.subscriptions.filter((s) => s.id === scopeSubscriptionId);
  if (!subs.length) return [];

  const usedBySub = new Map<string, Set<string>>();
  for (const inst of state.instances) {
    let set = usedBySub.get(inst.subscriptionId);
    if (!set) {
      set = new Set<string>();
      usedBySub.set(inst.subscriptionId, set);
    }
    set.add(inst.proxyName);
  }

  const candidates: PickedProxy[] = [];
  for (const sub of subs) {
    const used = usedBySub.get(sub.id) ?? new Set<string>();
    const health = loadProxyHealth(sub.id);
    for (const p of sub.proxies) {
      if (!p?.name) continue;
      if (used.has(p.name)) continue;
      candidates.push({
        subscriptionId: sub.id,
        subscription: sub,
        proxyName: p.name,
        proxy: p,
        health: health[p.name] ?? null
      });
    }
  }

  if (candidates.length === 0) return [];

  const groupRank = (h: HealthStatus | null): number => {
    if (!h) return 1; // 未检测
    return h.ok ? 0 : 2; // 可用优先，其次未测，最后不可用
  };
  const latencyValue = (h: HealthStatus | null): number => {
    if (!h) return Number.POSITIVE_INFINITY;
    if (typeof h.latencyMs !== "number" || !Number.isFinite(h.latencyMs)) return Number.POSITIVE_INFINITY;
    return h.latencyMs;
  };

  candidates.sort((a, b) => {
    const ga = groupRank(a.health);
    const gb = groupRank(b.health);
    if (ga !== gb) return ga - gb;

    const la = latencyValue(a.health);
    const lb = latencyValue(b.health);
    if (la !== lb) return la - lb;

    const sa = a.subscription.name.localeCompare(b.subscription.name, "zh-CN");
    if (sa !== 0) return sa;
    return a.proxyName.localeCompare(b.proxyName, "zh-CN");
  });

  return candidates;
}

function getInstalledMihomoPath(): string {
  const binPath = installer.getBinPath();
  if (!existsSync(binPath)) {
    throw new Error("mihomo 内核未安装，请先在「设置」中点击安装");
  }
  return binPath;
}

async function bootstrapAutoStart(): Promise<void> {
  const toStart = state.instances.filter((i) => i.autoStart);
  if (toStart.length === 0) return;
  const binPath = installer.getBinPath();
  if (!existsSync(binPath)) {
    console.warn("检测到存在 autoStart 实例，但 mihomo 内核尚未安装，已跳过自动启动。");
    return;
  }

  for (const inst of toStart) {
    try {
      await startInstanceWithPreflight(inst);
      console.log(`autoStart: 已启动 ${inst.id} (${inst.mixedPort})`);
    } catch (e) {
      console.warn(`autoStart: 启动失败 ${inst.id}: ${(e as Error).message}`);
    }
  }
}

let healthInterval: ReturnType<typeof setInterval> | null = null;
let healthAutoRunning = false;

async function runWithConcurrency<T>(items: readonly T[], limit: number, fn: (item: T) => Promise<void>): Promise<void> {
  const concurrency = Math.max(1, Math.min(limit, items.length));
  let idx = 0;
  const workers = new Array(concurrency).fill(0).map(async () => {
    while (true) {
      const i = idx++;
      if (i >= items.length) return;
      await fn(items[i]);
    }
  });
  await Promise.all(workers);
}

async function checkAllInstances(onlyRunning: boolean): Promise<void> {
  const list = onlyRunning
    ? state.instances.filter((i) => mihomo.getRuntimeStatus(i.id).running)
    : [...state.instances];

  await runWithConcurrency(list, 6, async (inst) => {
    try {
      const res = await mihomo.checkInstance(inst, state.settings);
      const m = loadProxyHealth(inst.subscriptionId);
      const key = typeof res.proxyName === "string" && res.proxyName.trim() ? res.proxyName.trim() : inst.proxyName;
      m[key] = res;
      saveProxyHealth(inst.subscriptionId, m);
    } catch (e) {
      console.warn(`healthcheck: ${inst.id} ${(e as Error).message}`);
    }
  });
}

async function autoHealthTick(): Promise<void> {
  if (healthAutoRunning) return;
  healthAutoRunning = true;
  try {
    await checkAllInstances(true);
  } finally {
    healthAutoRunning = false;
  }
}

function applyHealthSchedule(): void {
  if (healthInterval) {
    clearInterval(healthInterval);
    healthInterval = null;
  }
  const sec = Number(state.settings.healthCheckIntervalSec);
  if (!Number.isFinite(sec) || sec <= 0) return;
  const ms = sec * 1000;
  healthInterval = setInterval(() => {
    autoHealthTick().catch(() => {});
  }, ms);
  setTimeout(() => autoHealthTick().catch(() => {}), 800);
}

async function routeApi(req: Request): Promise<Response> {
  const url = new URL(req.url);
  const path = url.pathname;

  if (req.method === "POST" && path === "/api/login") {
    const body = (await req.json().catch(() => ({}))) as any;
    const username = typeof body?.username === "string" ? body.username : "";
    const password = typeof body?.password === "string" ? body.password : "";
    if (username === ADMIN.username && password === ADMIN.password) {
      const now = Math.floor(Date.now() / 1000);
      const token = signJwtHs256(
        { sub: ADMIN.username, iat: now, exp: now + JWT_TTL_SEC, iss: "mihomo-pool", jti: crypto.randomUUID() },
        JWT_SECRET
      );
      return json({ ok: true, token, tokenKey: AUTH_TOKEN_KEY, expiresInSec: JWT_TTL_SEC });
    }
    return json({ ok: false, error: "账号或密码错误" }, { status: 401 });
  }

  if (!isAuthorized(req)) return unauthorized();

  if (req.method === "GET" && path === "/api/mihomo/status") {
    return json({ ok: true, status: installer.getStatus() });
  }

  if (req.method === "POST" && path === "/api/mihomo/latest") {
    const body = (await req.json().catch(() => ({}))) as any;
    const includePrerelease = !!body?.includePrerelease;
    try {
      const latest = await installer.getLatestInfo(includePrerelease);
      return json({ ok: true, latest });
    } catch (e) {
      return badRequest((e as Error).message);
    }
  }

  if (req.method === "POST" && path === "/api/mihomo/install") {
    const body = (await req.json().catch(() => ({}))) as any;
    const includePrerelease = !!body?.includePrerelease;
    const force = !!body?.force;
    try {
      const installed = await installer.installLatest(includePrerelease, force);
      return json({ ok: true, installed });
    } catch (e) {
      return badRequest((e as Error).message);
    }
  }

  if (req.method === "GET" && path === "/api/state") {
    return json({
      ok: true,
      state: {
        ...state,
        instances: state.instances.map(withRuntime)
      }
    });
  }

  if (req.method === "GET" && path === "/api/settings") {
    return json({ ok: true, settings: state.settings });
  }

  if (req.method === "POST" && path === "/api/settings/reset-proxy-auth") {
    const enabled = !!state.settings?.proxyAuth?.enabled;
    const nextAuth = generateProxyAuth();
    nextAuth.enabled = enabled;

    state = { ...state, settings: { ...state.settings, proxyAuth: nextAuth } };
    await storage.saveState(state);

    return json({ ok: true, proxyAuth: nextAuth });
  }

  if (req.method === "PUT" && path === "/api/settings") {
    const body = (await req.json()) as any;
    if (typeof body !== "object" || !body) return badRequest("无效 JSON");

    const next = { ...state.settings };
    if (body.bindAddress !== undefined) next.bindAddress = String(body.bindAddress || "127.0.0.1");
    if (body.allowLan !== undefined) next.allowLan = !!body.allowLan;
    if (body.logLevel !== undefined) next.logLevel = String(body.logLevel) as any;
    if (body.baseMixedPort !== undefined) next.baseMixedPort = Number(body.baseMixedPort);
    if (body.baseControllerPort !== undefined) next.baseControllerPort = Number(body.baseControllerPort);
    if (body.maxLogLines !== undefined) next.maxLogLines = Number(body.maxLogLines);
    if (body.healthCheckIntervalSec !== undefined) {
      const v = Number(body.healthCheckIntervalSec);
      if (!Number.isFinite(v) || v < 0) return badRequest("自动检测间隔必须为非负数字（秒）");
      next.healthCheckIntervalSec = Math.floor(v);
    }
    if (body.healthCheckUrl !== undefined) {
      const v = String(body.healthCheckUrl || "").trim();
      if (!v) return badRequest("检测链接不能为空");
      let parsed: URL;
      try {
        parsed = new URL(v);
      } catch {
        return badRequest("检测链接不是合法 URL");
      }
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        return badRequest("检测链接只支持 http/https");
      }
      next.healthCheckUrl = v;
    }

    if (body.proxyAuth?.enabled !== undefined) {
      if (typeof body.proxyAuth.enabled !== "boolean") return badRequest("proxyAuth.enabled 必须为 boolean");
      next.proxyAuth = { ...next.proxyAuth, enabled: body.proxyAuth.enabled };
    }

    state = { ...state, settings: next };
    await storage.saveState(state);
    applyHealthSchedule();
    return json({ ok: true, settings: state.settings });
  }

  if (req.method === "GET" && path === "/api/subscriptions") {
    return json({ ok: true, subscriptions: state.subscriptions });
  }

  if (req.method === "POST" && path === "/api/subscriptions") {
    const body = (await req.json()) as any;
    const name = typeof body?.name === "string" ? body.name.trim() : "";
    const urlStr = typeof body?.url === "string" ? body.url.trim() : "";
    const rawYaml = typeof body?.yaml === "string" ? body.yaml.trim() : "";

    if (!name) return badRequest("name 不能为空");
    if (!urlStr && !rawYaml) return badRequest("url 或 yaml 需要至少提供一个");

    let yamlText = rawYaml;
    let lastError: string | null = null;
    if (!yamlText && urlStr) {
      try {
        const resp = await fetch(urlStr);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        yamlText = await resp.text();
      } catch (e) {
        lastError = `拉取订阅失败：${(e as Error).message}`;
      }
    }

    let proxies: any[] = [];
    if (yamlText) {
      try {
        proxies = parseSubscriptionYaml(yamlText).proxies;
      } catch (e) {
        lastError = (e as Error).message;
      }
    }

    const id = crypto.randomUUID();
    const createdAt = nowIso();
    const sub: Subscription = {
      id,
      name,
      url: urlStr || null,
      createdAt,
      updatedAt: createdAt,
      lastError,
      proxies
    };
    state = { ...state, subscriptions: [...state.subscriptions, sub] };
    await storage.saveState(state);

    if (yamlText) {
      await mkdir(join(DATA_DIR, "subscriptions"), { recursive: true });
      await Bun.write(join(DATA_DIR, "subscriptions", `${id}.yaml`), yamlText);
    }

    return json({ ok: true, subscription: sub });
  }

  if (req.method === "POST" && path.startsWith("/api/subscriptions/") && path.endsWith("/refresh")) {
    const id = path.split("/")[3];
    const sub = state.subscriptions.find((s) => s.id === id);
    if (!sub) return notFound();
    if (!sub.url) return badRequest("该订阅没有 url，无法刷新");

    try {
      const resp = await fetch(sub.url);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const yamlText = await resp.text();
      const proxies = parseSubscriptionYaml(yamlText).proxies;

      const updated: Subscription = { ...sub, updatedAt: nowIso(), lastError: null, proxies };
      state = {
        ...state,
        subscriptions: state.subscriptions.map((s) => (s.id === id ? updated : s))
      };
      await storage.saveState(state);

      await mkdir(join(DATA_DIR, "subscriptions"), { recursive: true });
      await Bun.write(join(DATA_DIR, "subscriptions", `${id}.yaml`), yamlText);

      return json({ ok: true, subscription: updated });
    } catch (e) {
      const updated: Subscription = { ...sub, updatedAt: nowIso(), lastError: (e as Error).message };
      state = {
        ...state,
        subscriptions: state.subscriptions.map((s) => (s.id === id ? updated : s))
      };
      await storage.saveState(state);
      return badRequest(`刷新失败：${(e as Error).message}`);
    }
  }

  if (req.method === "GET" && path.startsWith("/api/subscriptions/") && path.endsWith("/proxies")) {
    const id = path.split("/")[3];
    const sub = state.subscriptions.find((s) => s.id === id);
    if (!sub) return notFound();
    const health = loadProxyHealth(id);
    const proxies = sub.proxies.map((p) => ({ ...p, health: health[p.name] ?? null }));
    return json({ ok: true, proxies });
  }

  if (req.method === "GET" && path === "/api/subscriptions/availability") {
    const usedBySub = new Map<string, Set<string>>();
    for (const inst of state.instances) {
      let set = usedBySub.get(inst.subscriptionId);
      if (!set) {
        set = new Set<string>();
        usedBySub.set(inst.subscriptionId, set);
      }
      set.add(inst.proxyName);
    }

    let total = 0;
    let used = 0;
    let available = 0;
    let untested = 0;
    let unhealthy = 0;

    for (const sub of state.subscriptions) {
      const health = loadProxyHealth(sub.id);
      const usedSet = usedBySub.get(sub.id) ?? new Set<string>();
      total += sub.proxies.length;
      for (const p of sub.proxies) {
        if (!p?.name) continue;
        if (usedSet.has(p.name)) {
          used++;
          continue;
        }
        const h = health[p.name];
        if (!h) {
          untested++;
          continue;
        }
        if (h.ok) available++;
        else unhealthy++;
      }
    }

    return json({
      ok: true,
      availability: {
        subscriptionId: "all",
        total,
        used,
        available,
        untested,
        unhealthy,
        target: state.settings.healthCheckUrl
      }
    });
  }

  if (req.method === "GET" && path.startsWith("/api/subscriptions/") && path.endsWith("/availability")) {
    const id = path.split("/")[3];
    const sub = state.subscriptions.find((s) => s.id === id);
    if (!sub) return notFound();

    const health = loadProxyHealth(id);
    const used = new Set(state.instances.filter((i) => i.subscriptionId === id).map((i) => i.proxyName));
    let available = 0;
    let usedCount = 0;
    let untested = 0;
    let unhealthy = 0;

    for (const p of sub.proxies) {
      if (used.has(p.name)) {
        usedCount++;
        continue;
      }
      const h = health[p.name];
      if (!h) {
        untested++;
        continue;
      }
      if (h.ok) available++;
      else unhealthy++;
    }

    return json({
      ok: true,
      availability: {
        subscriptionId: id,
        total: sub.proxies.length,
        used: usedCount,
        available,
        untested,
        unhealthy,
        target: state.settings.healthCheckUrl
      }
    });
  }

  if (req.method === "POST" && path.startsWith("/api/subscriptions/") && path.endsWith("/proxies/check")) {
    const id = path.split("/")[3];
    const sub = state.subscriptions.find((s) => s.id === id);
    if (!sub) return notFound();

    const body = (await req.json().catch(() => ({}))) as any;
    const all = !!body?.all;
    const namesFromBody = Array.isArray(body?.names) ? body.names.filter((x: any) => typeof x === "string") : null;
    const proxyName = typeof body?.proxyName === "string" ? body.proxyName : null;

    let names: string[] = [];
    if (all) names = sub.proxies.map((p) => p.name);
    else if (namesFromBody?.length) names = namesFromBody.map((s: string) => s.trim()).filter(Boolean);
    else if (proxyName) names = [proxyName.trim()].filter(Boolean);
    else return badRequest("需要提供 all / names / proxyName");

    const nameSet = new Set(sub.proxies.map((p) => p.name));
    const invalid = names.filter((n) => !nameSet.has(n));
    if (invalid.length) return badRequest("存在未知节点", { invalid });

    let binPath: string;
    try {
      binPath = getInstalledMihomoPath();
    } catch (e) {
      return badRequest((e as Error).message);
    }

    const results: Record<string, HealthStatus> = {};
    const limit = Math.min(4, Math.max(1, names.length));
    await runWithConcurrency(names, limit, async (name) => {
      try {
        const res = await mihomo.checkSubscriptionProxyDelay(id, sub.updatedAt, sub.proxies, name, state.settings, binPath);
        results[name] = res;
      } catch (e) {
        results[name] = {
          ok: false,
          checkedAt: nowIso(),
          latencyMs: null,
          error: (e as Error).message,
          target: state.settings.healthCheckUrl
        };
      }
    });

    const current = loadProxyHealth(id);
    for (const [name, r] of Object.entries(results)) current[name] = r;
    saveProxyHealth(id, current);

    return json({ ok: true, results });
  }

  if (req.method === "GET" && path === "/api/instances") {
    return json({ ok: true, instances: state.instances.map(withRuntime) });
  }

  if (req.method === "PUT" && path.startsWith("/api/instances/")) {
    const parts = path.split("/");
    // /api/instances/:id
    if (parts.length === 4) {
      const id = parts[3];
      const inst = state.instances.find((i) => i.id === id);
      if (!inst) return notFound();

      const body = (await req.json().catch(() => ({}))) as any;
      if (!body || typeof body !== "object") return badRequest("无效 JSON");

      if (body.autoSwitch === undefined) return badRequest("缺少 autoSwitch");
      if (typeof body.autoSwitch !== "boolean") return badRequest("autoSwitch 必须为 boolean");

      const nextValue = body.autoSwitch;
      if (nextValue === inst.autoSwitch) return json({ ok: true, instance: withRuntime(inst) });

      const next: Instance = { ...inst, autoSwitch: nextValue, updatedAt: nowIso() };
      const running = mihomo.getRuntimeStatus(inst.id).running;

      if (running) {
        // 需要重启以写入新配置，失败则回滚保持当前可用性
        let binPath: string;
        try {
          binPath = getInstalledMihomoPath();
        } catch (e) {
          return badRequest((e as Error).message);
        }

        await mihomo.stopInstance(inst).catch(() => {});
        try {
          await startInstanceWithPreflight(next);
        } catch (e) {
          // 尝试恢复旧配置（尽量保持端口可用）
          await mihomo.start(inst, state.settings, binPath, getSubscriptionProxiesForInstance(inst)).catch(() => {});
          return badRequest(`更新失败：重启实例失败：${(e as Error).message}`);
        }
      }

      state = { ...state, instances: state.instances.map((i) => (i.id === id ? next : i)) };
      await storage.saveState(state);

      return json({ ok: true, instance: withRuntime(next) });
    }
  }

  if (req.method === "POST" && path === "/api/instances/batch") {
    const body = (await req.json().catch(() => ({}))) as any;
    const subscriptionIdRaw = typeof body?.subscriptionId === "string" ? body.subscriptionId.trim() : "";
    const count = Number(body?.count);
    const autoStart = !!body?.autoStart;
    const autoSwitch = body?.autoSwitch === undefined ? true : !!body?.autoSwitch;

    if (!Number.isInteger(count) || count < 1 || count > 200) return badRequest("count 非法（1-200）");

    type Candidate = { subscriptionId: string; subscription: Subscription; name: string; proxy: MihomoProxy; health: HealthStatus | null };

    const wantAll = isAllSubscriptionValue(subscriptionIdRaw);
    if (!wantAll && !subscriptionIdRaw) return badRequest("subscriptionId 不能为空");

    let chosen: Candidate[] = [];
    if (wantAll) {
      const usedBySub = new Map<string, Set<string>>();
      for (const inst of state.instances) {
        let set = usedBySub.get(inst.subscriptionId);
        if (!set) {
          set = new Set<string>();
          usedBySub.set(inst.subscriptionId, set);
        }
        set.add(inst.proxyName);
      }

      const candidates: Candidate[] = [];
      for (const sub of state.subscriptions) {
        const health = loadProxyHealth(sub.id);
        const used = usedBySub.get(sub.id) ?? new Set<string>();
        for (const p of sub.proxies) {
          if (!p?.name) continue;
          if (used.has(p.name)) continue;
          const h = health[p.name] ?? null;
          if (!h?.ok) continue;
          candidates.push({ subscriptionId: sub.id, subscription: sub, name: p.name, proxy: p, health: h });
        }
      }

      candidates.sort((a, b) => {
        const da = typeof a.health?.latencyMs === "number" ? a.health.latencyMs : Number.POSITIVE_INFINITY;
        const db = typeof b.health?.latencyMs === "number" ? b.health.latencyMs : Number.POSITIVE_INFINITY;
        if (da !== db) return da - db;
        const sa = a.subscription.name.localeCompare(b.subscription.name, "zh-CN");
        if (sa !== 0) return sa;
        return a.name.localeCompare(b.name, "zh-CN");
      });

      if (candidates.length < count) {
        let total = 0;
        let used = 0;
        let untested = 0;
        let unhealthy = 0;
        for (const sub of state.subscriptions) {
          const health = loadProxyHealth(sub.id);
          const usedSet = usedBySub.get(sub.id) ?? new Set<string>();
          total += sub.proxies.length;
          for (const p of sub.proxies) {
            if (!p?.name) continue;
            if (usedSet.has(p.name)) {
              used++;
              continue;
            }
            const h = health[p.name];
            if (!h) untested++;
            else if (!h.ok) unhealthy++;
          }
        }
        return badRequest("可用节点不足，请先在「订阅」->「节点」中进行检测", {
          requested: count,
          available: candidates.length,
          total,
          used,
          untested,
          unhealthy,
          target: state.settings.healthCheckUrl
        });
      }

      chosen = candidates.slice(0, count);
    } else {
      const subscriptionId = subscriptionIdRaw;
      const sub = state.subscriptions.find((s) => s.id === subscriptionId);
      if (!sub) return badRequest("subscriptionId 不存在");

      const health = loadProxyHealth(subscriptionId);
      const used = new Set(state.instances.filter((i) => i.subscriptionId === subscriptionId).map((i) => i.proxyName));
      const availableCandidates: Candidate[] = sub.proxies
        .filter((p) => !used.has(p.name))
        .map((p) => ({ subscriptionId, subscription: sub, name: p.name, proxy: p, health: health[p.name] ?? null }))
        .filter((x) => x.health?.ok)
        .sort((a, b) => {
          const da = typeof a.health?.latencyMs === "number" ? a.health.latencyMs : Number.POSITIVE_INFINITY;
          const db = typeof b.health?.latencyMs === "number" ? b.health.latencyMs : Number.POSITIVE_INFINITY;
          return da - db;
        });

      if (availableCandidates.length < count) {
        let untested = 0;
        let unhealthy = 0;
        for (const p of sub.proxies) {
          if (used.has(p.name)) continue;
          const h = health[p.name];
          if (!h) untested++;
          else if (!h.ok) unhealthy++;
        }
        return badRequest("可用节点不足，请先在「订阅」->「节点」中进行检测", {
          requested: count,
          available: availableCandidates.length,
          total: sub.proxies.length,
          used: used.size,
          untested,
          unhealthy,
          target: state.settings.healthCheckUrl
        });
      }

      chosen = availableCandidates.slice(0, count);
    }
    const reserved = collectReservedPorts();
    const bindHost = state.settings.bindAddress || "127.0.0.1";
    let nextMixedStart = state.settings.baseMixedPort;
    let nextCtrlStart = state.settings.baseControllerPort;

    const createdAt = nowIso();
    const createdInstances: Instance[] = [];
    for (const c of chosen) {
      const mixedPort = await findNextFreePortAvoiding(nextMixedStart, reserved, bindHost);
      reserved.add(mixedPort);
      nextMixedStart = mixedPort + 1;

      const controllerPort = await findNextFreePortAvoiding(nextCtrlStart, reserved, "127.0.0.1");
      reserved.add(controllerPort);
      nextCtrlStart = controllerPort + 1;

      const inst: Instance = {
        id: crypto.randomUUID(),
        name: `${c.subscription.name} / ${c.name}`,
        subscriptionId: c.subscriptionId,
        proxyName: c.name,
        proxy: c.proxy,
        mixedPort,
        controllerPort,
        autoStart,
        autoSwitch,
        createdAt,
        updatedAt: createdAt
      };
      createdInstances.push(inst);
    }

    state = { ...state, instances: [...state.instances, ...createdInstances] };
    await storage.saveState(state);

    let startErrors: Record<string, string> = {};
    if (autoStart) {
      let binPath: string;
      try {
        binPath = getInstalledMihomoPath();
      } catch (e) {
        return badRequest((e as Error).message, { created: createdInstances.map(withRuntime) });
      }

      for (const inst of createdInstances) {
        try {
          await startInstanceWithPreflight(inst);
        } catch (e) {
          startErrors[inst.id] = (e as Error).message;
        }
      }
    }

    return json({
      ok: true,
      created: createdInstances.map(withRuntime),
      startErrors
    });
  }

  if (req.method === "POST" && path === "/api/instances/check-all") {
    await checkAllInstances(true);
    return json({ ok: true, instances: state.instances.map(withRuntime) });
  }

  if (req.method === "POST" && path === "/api/instances") {
    const body = (await req.json()) as any;
    const subscriptionIdRaw = typeof body?.subscriptionId === "string" ? body.subscriptionId.trim() : "";
    const proxyNameRaw = typeof body?.proxyName === "string" ? body.proxyName.trim() : "";
    const requestedPort = body?.mixedPort !== undefined ? Number(body.mixedPort) : null;
    const autoStart = !!body?.autoStart;
    const autoSwitch = body?.autoSwitch === undefined ? true : !!body?.autoSwitch;

    const wantAuto = isAutoProxyValue(proxyNameRaw);
    const scopeSubId = isAllSubscriptionValue(subscriptionIdRaw) ? "" : subscriptionIdRaw;

    let sub: Subscription;
    let proxy: MihomoProxy;
    let subscriptionId: string;
    let proxyName: string;

    // 创建实例前必须先做可用性检测（依赖已安装的 mihomo 内核）
    let binPath: string;
    try {
      binPath = getInstalledMihomoPath();
    } catch (e) {
      return badRequest(`创建实例前需要先安装 mihomo 内核以进行节点检测：${(e as Error).message}`);
    }

    if (wantAuto) {
      const candidates = listUnusedProxyCandidates(scopeSubId);
      if (candidates.length === 0) {
        return badRequest("没有找到未被占用的节点（请先导入订阅，或删除旧实例释放节点）");
      }

      let picked: PickedProxy | null = null;
      for (const c of candidates) {
        const res = await checkAndSaveProxyHealth(c.subscription, c.proxyName, binPath);
        if (res.ok) {
          picked = { ...c, health: res };
          break;
        }
      }

      if (!picked) {
        const wantAll = isAllSubscriptionValue(scopeSubId);
        const subs = wantAll ? state.subscriptions : state.subscriptions.filter((s) => s.id === scopeSubId);
        const usedBySub = new Map<string, Set<string>>();
        for (const inst of state.instances) {
          let set = usedBySub.get(inst.subscriptionId);
          if (!set) {
            set = new Set<string>();
            usedBySub.set(inst.subscriptionId, set);
          }
          set.add(inst.proxyName);
        }

        let total = 0;
        let used = 0;
        let untested = 0;
        let unhealthy = 0;
        for (const s of subs) {
          const health = loadProxyHealth(s.id);
          const usedSet = usedBySub.get(s.id) ?? new Set<string>();
          total += s.proxies.length;
          for (const p of s.proxies) {
            if (!p?.name) continue;
            if (usedSet.has(p.name)) {
              used++;
              continue;
            }
            const h = health[p.name];
            if (!h) untested++;
            else if (!h.ok) unhealthy++;
          }
        }

        return badRequest("没有找到可用节点，请先在「订阅」->「节点」中进行检测", {
          total,
          used,
          untested,
          unhealthy,
          target: state.settings.healthCheckUrl
        });
      }

      sub = picked.subscription;
      proxy = picked.proxy;
      subscriptionId = picked.subscriptionId;
      proxyName = picked.proxyName;
    } else {
      if (isAllSubscriptionValue(subscriptionIdRaw)) {
        return badRequest("选择了具体节点时，必须同时指定 subscriptionId");
      }
      const foundSub = state.subscriptions.find((s) => s.id === subscriptionIdRaw);
      if (!foundSub) return badRequest("subscriptionId 不存在");
      const foundProxy = foundSub.proxies.find((p) => p.name === proxyNameRaw);
      if (!foundProxy) return badRequest("proxyName 不存在或不在订阅里");
      if (state.instances.some((i) => i.subscriptionId === subscriptionIdRaw && i.proxyName === proxyNameRaw)) {
        return badRequest("该节点已被某个实例占用，请先删除旧实例或选择其他节点");
      }

      const health = await checkAndSaveProxyHealth(foundSub, proxyNameRaw, binPath);
      if (!health.ok) {
        return badRequest(`节点不可用，创建已取消：${health.error || "检测失败"}`, { health });
      }

      sub = foundSub;
      proxy = foundProxy;
      subscriptionId = subscriptionIdRaw;
      proxyName = proxyNameRaw;
    }

    const bindHost = state.settings.bindAddress || "127.0.0.1";
    const reserved = collectReservedPorts();

    let mixedPort: number;
    if (requestedPort !== null) {
      if (!Number.isInteger(requestedPort) || requestedPort < 1 || requestedPort > 65535) {
        return badRequest("mixedPort 非法（1-65535）");
      }
      if (reserved.has(requestedPort)) return badRequest("mixedPort 已被其他实例占用（配置层面）");
      if (!(await isPortFree(requestedPort, bindHost))) return badRequest("mixedPort 已被系统占用（端口监听冲突）");
      mixedPort = requestedPort;
    } else {
      mixedPort = await findNextFreePortAvoiding(state.settings.baseMixedPort, reserved, bindHost);
    }

    reserved.add(mixedPort);
    const controllerPort = await findNextFreePortAvoiding(state.settings.baseControllerPort, reserved, "127.0.0.1");

    const id = crypto.randomUUID();
    const createdAt = nowIso();
    const inst: Instance = {
      id,
      name: `${sub.name} / ${proxyName}`,
      subscriptionId,
      proxyName,
      proxy,
      mixedPort,
      controllerPort,
      autoStart,
      autoSwitch,
      createdAt,
      updatedAt: createdAt
    };

    state = { ...state, instances: [...state.instances, inst] };
    await storage.saveState(state);

    if (autoStart) {
      try {
        await startInstanceWithPreflight(inst);
      } catch (e) {
        return badRequest(`创建成功但启动失败：${(e as Error).message}`, { instance: inst });
      }
    }

    return json({ ok: true, instance: withRuntime(inst) });
  }

  if (req.method === "POST" && path.startsWith("/api/instances/") && path.endsWith("/start")) {
    const id = path.split("/")[3];
    const inst = state.instances.find((i) => i.id === id);
    if (!inst) return notFound();
    try {
      await startInstanceWithPreflight(inst);
      return json({ ok: true, instance: withRuntime(inst) });
    } catch (e) {
      return badRequest((e as Error).message);
    }
  }

  if (req.method === "POST" && path.startsWith("/api/instances/") && path.endsWith("/stop")) {
    const id = path.split("/")[3];
    const inst = state.instances.find((i) => i.id === id);
    if (!inst) return notFound();
    try {
      await mihomo.stopInstance(inst);
    } catch (e) {
      return badRequest((e as Error).message);
    }
    return json({ ok: true, instance: withRuntime(inst) });
  }

  if (req.method === "GET" && path.startsWith("/api/instances/") && path.endsWith("/logs")) {
    const id = path.split("/")[3];
    const inst = state.instances.find((i) => i.id === id);
    if (!inst) return notFound();
    return json({ ok: true, lines: mihomo.getLogs(id) });
  }

  if (req.method === "POST" && path.startsWith("/api/instances/") && path.endsWith("/check")) {
    const id = path.split("/")[3];
    const inst = state.instances.find((i) => i.id === id);
    if (!inst) return notFound();
    const health = await mihomo.checkInstance(inst, state.settings);
    const m = loadProxyHealth(inst.subscriptionId);
    const key = typeof health.proxyName === "string" && health.proxyName.trim() ? health.proxyName.trim() : inst.proxyName;
    m[key] = health;
    saveProxyHealth(inst.subscriptionId, m);
    return json({ ok: true, health });
  }

  if (req.method === "DELETE" && path.startsWith("/api/instances/")) {
    const id = path.split("/")[3];
    const inst = state.instances.find((i) => i.id === id);
    if (!inst) return notFound();
    // 删除实例时“静默”停止：即使停止/清理失败也继续删除定义
    await mihomo.stopInstance(inst).catch(() => {});
    state = { ...state, instances: state.instances.filter((i) => i.id !== id) };
    await storage.saveState(state);
    return json({ ok: true });
  }

  if (req.method === "GET" && path === "/api/pool") {
    const host = (process.env.PROXY_HOST ?? "127.0.0.1").trim();
    const list = state.instances.map((i) => ({
      id: i.id,
      name: i.name,
      mixedPort: i.mixedPort,
      proxy: `${host}:${i.mixedPort}`,
      running: mihomo.getRuntimeStatus(i.id).running
    }));
    return json({ ok: true, proxies: list });
  }

  return notFound();
}

async function routeStatic(req: Request): Promise<Response> {
  const url = new URL(req.url);
  const pathname = url.pathname === "/" ? "/index.html" : url.pathname;
  const filePath = resolveUnder(WEB_DIR, pathname);
  if (!filePath) return notFound();
  if (!existsSync(filePath)) return notFound();
  return new Response(Bun.file(filePath));
}

const hostname = process.env.HOST ?? "127.0.0.1";
const port = Number(process.env.PORT ?? "3320");

const server = Bun.serve({
  hostname,
  port,
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname.startsWith("/api/")) return await routeApi(req);
    return await routeStatic(req);
  }
});

async function shutdown() {
  console.log("正在停止所有实例...");
  await mihomo.stopAll();
  if (healthInterval) clearInterval(healthInterval);
  server.stop(true);
  process.exit(0);
}

process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);

console.log(`mihomo-pool 已启动：http://${hostname}:${port}`);
console.log("登录信息（首次生成并持久化）：");
console.log(`账号: ${ADMIN.username}`);
console.log(`密码: ${ADMIN.password}`);

bootstrapAutoStart();
applyHealthSchedule();
