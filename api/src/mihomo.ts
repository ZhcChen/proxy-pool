import { access, mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import net from "node:net";
import YAML from "yaml";
import type { Instance, MihomoProxy, Settings } from "./types";
import { ensureDir, nowIso } from "./utils";

type RunningProcess = {
  id: string;
  pid: number;
  startedAt: string;
  proc: Bun.Subprocess<"pipe", "pipe", "pipe">;
  logLines: string[];
};

export type HealthStatus = {
  ok: boolean;
  checkedAt: string;
  latencyMs: number | null;
  statusCode?: number;
  error?: string;
  target?: string;
  proxyName?: string;
};

type DelayApiResponse = { delay?: number };

type ProxyCheckerProc = {
  subId: string;
  version: string;
  dir: string;
  controllerPort: number;
  proc: Bun.Subprocess<"pipe", "pipe", "pipe">;
  lastUsedAt: number;
  stopTimer: ReturnType<typeof setTimeout> | null;
  ready: Promise<void>;
};

export class MihomoManager {
  readonly dataDir: string;
  private running = new Map<string, RunningProcess>();
  private health = new Map<string, HealthStatus>();
  private proxyCheckers = new Map<string, ProxyCheckerProc>();
  private proxyCheckerStarts = new Map<string, Promise<ProxyCheckerProc>>();

  constructor(dataDir: string) {
    this.dataDir = dataDir;
  }

  getRuntimeStatus(instanceId: string): { running: boolean; pid: number | null; startedAt: string | null } {
    const rp = this.running.get(instanceId);
    return { running: !!rp, pid: rp?.pid ?? null, startedAt: rp?.startedAt ?? null };
  }

  getLogs(instanceId: string): string[] {
    return this.running.get(instanceId)?.logLines ?? [];
  }

  getHealthStatus(instanceId: string): HealthStatus | null {
    return this.health.get(instanceId) ?? null;
  }

  async verifyMihomoBinary(mihomoPath: string): Promise<void> {
    try {
      await access(mihomoPath);
    } catch {
      throw new Error(`找不到 mihomo 可执行文件：${mihomoPath}`);
    }
  }

  private instanceDir(instanceId: string): string {
    return join(this.dataDir, "instances", instanceId);
  }

  private configPath(instanceId: string): string {
    return join(this.instanceDir(instanceId), "config.yaml");
  }

  async writeConfig(
    instance: Instance,
    settings: Settings,
    subscriptionProxies: MihomoProxy[] = [],
    preferredProxyName: string | null = null
  ): Promise<void> {
    const dir = this.instanceDir(instance.id);
    await ensureDir(dir);

    const proxyName = instance.proxyName;
    const proxy = instance.proxy;

    const autoSwitch = !!instance.autoSwitch;

    const proxyByName = new Map<string, MihomoProxy>();
    if (Array.isArray(subscriptionProxies)) {
      for (const p of subscriptionProxies) {
        const name = typeof (p as any)?.name === "string" ? String((p as any).name).trim() : "";
        if (!name) continue;
        if (!proxyByName.has(name)) proxyByName.set(name, p);
      }
    }
    // 确保实例当前绑定的节点一定存在于配置里（即使订阅列表发生变化）
    if (proxyName) proxyByName.set(proxyName, proxy);

    const primary = preferredProxyName && proxyByName.has(preferredProxyName) ? preferredProxyName : proxyName;

    const orderedNames = primary
      ? [primary, ...Array.from(proxyByName.keys()).filter((n) => n !== primary)]
      : Array.from(proxyByName.keys());

    const proxies = (autoSwitch ? orderedNames : [proxyName])
      .map((n) => proxyByName.get(n))
      .filter((p): p is MihomoProxy => !!p);

    const group: Record<string, unknown> = autoSwitch
      ? {
          name: "PROXY",
          type: "fallback",
          proxies: orderedNames,
          url: settings.healthCheckUrl,
          interval: settings.healthCheckIntervalSec > 0 ? settings.healthCheckIntervalSec : 60
        }
      : {
          name: "PROXY",
          type: "select",
          proxies: [proxyName]
        };

    const cfg: Record<string, unknown> = {
      "mixed-port": instance.mixedPort,
      "allow-lan": settings.allowLan,
      "bind-address": settings.bindAddress,
      mode: "rule",
      "log-level": settings.logLevel,
      "external-controller": `127.0.0.1:${instance.controllerPort}`,
      ...(settings.proxyAuth?.enabled
        ? { authentication: [`${settings.proxyAuth.username}:${settings.proxyAuth.password}`] }
        : {}),
      proxies,
      "proxy-groups": [group],
      rules: ["MATCH,PROXY"]
    };

    const yamlText = YAML.stringify(cfg);
    await writeFile(this.configPath(instance.id), yamlText, "utf8");
  }

  async start(
    instance: Instance,
    settings: Settings,
    mihomoPath: string,
    subscriptionProxies: MihomoProxy[] = [],
    preferredProxyName: string | null = null
  ): Promise<void> {
    if (this.running.has(instance.id)) return;
    if (!mihomoPath) throw new Error("mihomo 内核未安装");
    await this.verifyMihomoBinary(mihomoPath);

    const dir = this.instanceDir(instance.id);
    await mkdir(dir, { recursive: true });

    await this.writeConfig(instance, settings, subscriptionProxies, preferredProxyName);

    const configPath = this.configPath(instance.id);
    const args = ["-d", dir, "-f", configPath];

    const proc = Bun.spawn([mihomoPath, ...args], {
      cwd: dir,
      stdout: "pipe",
      stderr: "pipe",
      stdin: "pipe"
    });

    const startedAt = nowIso();
    const runningProc: RunningProcess = {
      id: instance.id,
      pid: proc.pid,
      startedAt,
      proc,
      logLines: []
    };
    this.running.set(instance.id, runningProc);

    this.pumpLogs(instance.id, proc.stdout, settings.maxLogLines, "[stdout]");
    this.pumpLogs(instance.id, proc.stderr, settings.maxLogLines, "[stderr]");

    proc.exited
      .then((code) => {
        const rp = this.running.get(instance.id);
        if (rp) {
          rp.logLines.push(`${nowIso()} [exit] code=${code}`);
        }
      })
      .finally(() => {
        this.running.delete(instance.id);
      });
  }

  async checkInstance(instance: Instance, settings: Settings): Promise<HealthStatus> {
    const checkedAt = nowIso();
    const target = String(settings.healthCheckUrl || "").trim();
    const timeoutMs = 5000;

    if (!this.running.has(instance.id)) {
      const res: HealthStatus = { ok: false, checkedAt, latencyMs: null, error: "实例未运行", target, proxyName: instance.proxyName };
      this.health.set(instance.id, res);
      return res;
    }

    let probeProxyName = instance.proxyName;
    if (instance.autoSwitch) {
      try {
        const resp = await fetch(`http://127.0.0.1:${instance.controllerPort}/proxies`);
        if (resp.ok) {
          const data = (await resp.json().catch(() => ({}))) as { proxies?: Record<string, { now?: unknown }> };
          const now = data?.proxies?.PROXY?.now;
          if (typeof now === "string" && now.trim()) probeProxyName = now.trim();
        }
      } catch {
        // ignore
      }
    }

    const url =
      `http://127.0.0.1:${instance.controllerPort}` +
      `/proxies/${encodeURIComponent(probeProxyName)}` +
      `/delay?timeout=${timeoutMs}&url=${encodeURIComponent(target)}`;

    const ac = new AbortController();
    const timer = setTimeout(() => ac.abort(), timeoutMs + 2500);
    try {
      const resp = await fetch(url, { signal: ac.signal });
      const data = (await resp.json().catch(() => ({}))) as DelayApiResponse & { message?: string };
      if (!resp.ok) {
        const res: HealthStatus = {
          ok: false,
          checkedAt,
          latencyMs: null,
          error: data?.message ? String(data.message) : `HTTP ${resp.status}`,
          target,
          proxyName: probeProxyName
        };
        this.health.set(instance.id, res);
        return res;
      }
      if (typeof data.delay === "number" && Number.isFinite(data.delay)) {
        if (data.delay <= 0) {
          const res: HealthStatus = {
            ok: false,
            checkedAt,
            latencyMs: data.delay,
            error: "不可用（delay=0）",
            target,
            proxyName: probeProxyName
          };
          this.health.set(instance.id, res);
          return res;
        }
        const res: HealthStatus = { ok: true, checkedAt, latencyMs: data.delay, target, proxyName: probeProxyName };
        this.health.set(instance.id, res);
        return res;
      }
      const res: HealthStatus = {
        ok: false,
        checkedAt,
        latencyMs: null,
        error: "delay 响应缺少数值",
        target,
        proxyName: probeProxyName
      };
      this.health.set(instance.id, res);
      return res;
    } catch (e) {
      const res: HealthStatus = {
        ok: false,
        checkedAt,
        latencyMs: null,
        error: (e as Error).message,
        target,
        proxyName: probeProxyName
      };
      this.health.set(instance.id, res);
      return res;
    } finally {
      clearTimeout(timer);
    }
  }

  async stop(instanceId: string, timeoutMs = 5000): Promise<void> {
    const rp = this.running.get(instanceId);
    if (!rp) return;

    try {
      rp.proc.kill("SIGTERM");
    } catch {
      this.running.delete(instanceId);
      return;
    }

    const done = await Promise.race([
      rp.proc.exited.then(() => true),
      new Promise<boolean>((r) => setTimeout(() => r(false), timeoutMs))
    ]);
    if (!done) {
      try {
        rp.proc.kill("SIGKILL");
      } catch {
        // ignore
      }
    }
  }

  private async listListeningPids(port: number): Promise<number[]> {
    if (!Number.isInteger(port) || port <= 0 || port > 65535) return [];

    if (process.platform === "win32") {
      try {
        const ps = Bun.spawn(
          [
            "powershell",
            "-NoProfile",
            "-Command",
            `Get-NetTCPConnection -State Listen -LocalPort ${port} | Select-Object -ExpandProperty OwningProcess`
          ],
          { stdout: "pipe", stderr: "pipe" }
        );
        const code = await ps.exited;
        if (code !== 0) return [];
        const out = (await new Response(ps.stdout).text()).trim();
        const pids = out
          .split(/\r?\n/)
          .map((s) => Number(String(s).trim()))
          .filter((n) => Number.isInteger(n) && n > 0);
        return Array.from(new Set(pids));
      } catch {
        return [];
      }
    }

    try {
      const proc = Bun.spawn(["lsof", "-nP", `-iTCP:${port}`, "-sTCP:LISTEN", "-t"], { stdout: "pipe", stderr: "pipe" });
      const code = await proc.exited;
      if (code !== 0) return [];
      const out = (await new Response(proc.stdout).text()).trim();
      const pids = out
        .split(/\s+/)
        .map((s) => Number(String(s).trim()))
        .filter((n) => Number.isInteger(n) && n > 0);
      return Array.from(new Set(pids));
    } catch {
      return [];
    }
  }

  private tryKillPid(pid: number, signal: NodeJS.Signals): boolean {
    if (!Number.isInteger(pid) || pid <= 0) return false;
    if (pid === process.pid) return false;
    try {
      process.kill(pid, signal);
      return true;
    } catch {
      return false;
    }
  }

  private async killProcessesListeningOnPort(port: number): Promise<number[]> {
    const pids = await this.listListeningPids(port);
    if (pids.length === 0) return [];

    const killed: number[] = [];
    for (const pid of pids) {
      if (this.tryKillPid(pid, "SIGKILL")) killed.push(pid);
      else this.tryKillPid(pid, "SIGTERM");
    }
    return killed;
  }

  async stopInstance(instance: Pick<Instance, "id" | "mixedPort" | "controllerPort">, timeoutMs = 5000): Promise<void> {
    try {
      await this.stop(instance.id, timeoutMs);
    } catch {
      // ignore; fallthrough to port-based cleanup
    }

    const ports = Array.from(new Set([instance.mixedPort, instance.controllerPort].filter((p) => Number.isInteger(p) && p > 0)));
    if (ports.length === 0) return;

    // 如果进程未被追踪（例如管理器重启后遗留），按端口查找并强制 kill
    for (const port of ports) {
      const before = await this.listListeningPids(port);
      if (before.length === 0) continue;

      await this.killProcessesListeningOnPort(port);
      // 等待端口释放（最多 1.6s）
      const deadline = Date.now() + 1600;
      while (Date.now() < deadline) {
        const after = await this.listListeningPids(port);
        if (after.length === 0) break;
        await new Promise((r) => setTimeout(r, 120));
      }
    }
  }

  async stopAll(): Promise<void> {
    await Promise.allSettled([...this.running.keys()].map((id) => this.stop(id)));
    await Promise.allSettled([...this.proxyCheckers.keys()].map((subId) => this.stopProxyChecker(subId)));
  }

  private async pickEphemeralPort(host = "127.0.0.1"): Promise<number> {
    return await new Promise<number>((resolve, reject) => {
      const srv = net.createServer();
      srv.once("error", reject);
      srv.listen(0, host, () => {
        const addr = srv.address();
        const port = typeof addr === "object" && addr ? addr.port : 0;
        srv.close(() => resolve(port));
      });
    });
  }

  private async waitForControllerReady(controllerPort: number, timeoutMs = 8000): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    const url = `http://127.0.0.1:${controllerPort}/proxies`;
    while (Date.now() < deadline) {
      try {
        const resp = await fetch(url);
        if (resp.ok) return;
      } catch {
        // ignore
      }
      await new Promise((r) => setTimeout(r, 120));
    }
    throw new Error("检测器启动超时（controller 未就绪）");
  }

  private scheduleStopProxyChecker(subId: string): void {
    const checker = this.proxyCheckers.get(subId);
    if (!checker) return;
    checker.lastUsedAt = Date.now();
    if (checker.stopTimer) clearTimeout(checker.stopTimer);
    checker.stopTimer = setTimeout(() => {
      this.stopProxyChecker(subId).catch(() => {});
    }, 2 * 60 * 1000);
  }

  private async stopProxyChecker(subId: string): Promise<void> {
    const checker = this.proxyCheckers.get(subId);
    if (!checker) return;
    if (checker.stopTimer) clearTimeout(checker.stopTimer);
    try {
      checker.proc.kill("SIGTERM");
    } catch {
      // ignore
    }
    const done = await Promise.race([
      checker.proc.exited.then(() => true),
      new Promise<boolean>((r) => setTimeout(() => r(false), 2500))
    ]);
    if (!done) {
      try {
        checker.proc.kill("SIGKILL");
      } catch {
        // ignore
      }
    }
    this.proxyCheckers.delete(subId);
  }

  private async ensureProxyChecker(
    subId: string,
    proxies: MihomoProxy[],
    version: string,
    mihomoPath: string
  ): Promise<ProxyCheckerProc> {
    const existing = this.proxyCheckers.get(subId);
    if (existing && existing.version === version) {
      this.scheduleStopProxyChecker(subId);
      return existing;
    }
    if (existing) {
      await this.stopProxyChecker(subId);
    }

    const pending = this.proxyCheckerStarts.get(subId);
    if (pending) return await pending;

    const start = (async (): Promise<ProxyCheckerProc> => {
      await this.verifyMihomoBinary(mihomoPath);

      const dir = join(this.dataDir, "proxy-checkers", subId);
      await ensureDir(dir);

      const controllerPort = await this.pickEphemeralPort("127.0.0.1");
      const mixedPort = await this.pickEphemeralPort("127.0.0.1");
      const configPath = join(dir, "config.yaml");

      const cfg: Record<string, unknown> = {
        "mixed-port": mixedPort,
        "allow-lan": false,
        "bind-address": "127.0.0.1",
        mode: "rule",
        "log-level": "warning",
        "external-controller": `127.0.0.1:${controllerPort}`,
        secret: "",
        proxies,
        rules: ["MATCH,DIRECT"]
      };

      await writeFile(configPath, YAML.stringify(cfg), "utf8");

      const proc = Bun.spawn([mihomoPath, "-d", dir, "-f", configPath], {
        cwd: dir,
        stdout: "pipe",
        stderr: "pipe",
        stdin: "pipe"
      });

      const checker: ProxyCheckerProc = {
        subId,
        version,
        dir,
        controllerPort,
        proc,
        lastUsedAt: Date.now(),
        stopTimer: null,
        ready: this.waitForControllerReady(controllerPort)
      };

      proc.exited.finally(() => {
        this.proxyCheckers.delete(subId);
      });

      try {
        await checker.ready;
        this.proxyCheckers.set(subId, checker);
        this.scheduleStopProxyChecker(subId);
        return checker;
      } catch (e) {
        try {
          proc.kill("SIGKILL");
        } catch {
          // ignore
        }
        throw e;
      }
    })();

    this.proxyCheckerStarts.set(subId, start);
    try {
      return await start;
    } finally {
      this.proxyCheckerStarts.delete(subId);
    }
  }

  async checkSubscriptionProxyDelay(
    subscriptionId: string,
    subscriptionVersion: string,
    proxies: MihomoProxy[],
    proxyName: string,
    settings: Settings,
    mihomoPath: string
  ): Promise<HealthStatus> {
    const checkedAt = nowIso();
    const target = String(settings.healthCheckUrl || "").trim();
    const timeoutMs = 5000;

    if (!target) {
      return { ok: false, checkedAt, latencyMs: null, error: "未配置检测链接", target };
    }

    const checker = await this.ensureProxyChecker(subscriptionId, proxies, subscriptionVersion, mihomoPath);
    const url =
      `http://127.0.0.1:${checker.controllerPort}` +
      `/proxies/${encodeURIComponent(proxyName)}` +
      `/delay?timeout=${timeoutMs}&url=${encodeURIComponent(target)}`;

    const ac = new AbortController();
    const timer = setTimeout(() => ac.abort(), timeoutMs + 2500);
    try {
      const resp = await fetch(url, { signal: ac.signal });
      const data = (await resp.json().catch(() => ({}))) as DelayApiResponse & { message?: string };
      if (!resp.ok) {
        return {
          ok: false,
          checkedAt,
          latencyMs: null,
          error: data?.message ? String(data.message) : `HTTP ${resp.status}`,
          target
        };
      }
      if (typeof data.delay === "number" && Number.isFinite(data.delay)) {
        if (data.delay <= 0) {
          return { ok: false, checkedAt, latencyMs: data.delay, error: "不可用（delay=0）", target };
        }
        return { ok: true, checkedAt, latencyMs: data.delay, target };
      }
      return { ok: false, checkedAt, latencyMs: null, error: "delay 响应缺少数值", target };
    } catch (e) {
      return { ok: false, checkedAt, latencyMs: null, error: (e as Error).message, target };
    } finally {
      clearTimeout(timer);
      this.scheduleStopProxyChecker(subscriptionId);
    }
  }

  private async pumpLogs(
    instanceId: string,
    stream: ReadableStream<Uint8Array> | null,
    maxLines: number,
    tag: string
  ): Promise<void> {
    if (!stream) return;
    const decoder = new TextDecoder();
    const reader = stream.getReader();
    let buffer = "";

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split(/\r?\n/);
      buffer = parts.pop() ?? "";

      const rp = this.running.get(instanceId);
      if (!rp) continue;
      for (const line of parts) {
        rp.logLines.push(`${nowIso()} ${tag} ${line}`);
        if (rp.logLines.length > maxLines) rp.logLines.splice(0, rp.logLines.length - maxLines);
      }
    }
  }
}
