import { mkdir, rm, chmod, rename, writeFile, readdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { gunzipSync } from "node:zlib";
import type { Storage } from "./storage";
import { nowIso } from "./utils";

type GithubReleaseAsset = {
  name: string;
  browser_download_url: string;
  size: number;
};

type GithubRelease = {
  tag_name: string;
  name: string;
  prerelease: boolean;
  published_at: string;
  assets: GithubReleaseAsset[];
};

export type MihomoSystem = {
  os: "darwin" | "linux" | "windows" | string;
  arch: "amd64" | "arm64" | string;
};

export type MihomoInstallInfo = {
  repo: string;
  tag: string;
  assetName: string;
  installedAt: string;
  binPath: string;
  system: MihomoSystem;
};

export type MihomoStatus = {
  repo: string;
  system: MihomoSystem;
  binPath: string;
  installed: MihomoInstallInfo | null;
};

export type MihomoLatest = {
  tag: string;
  prerelease: boolean;
  publishedAt: string;
  assetName: string;
  downloadUrl: string;
};

function platformToOs(platform: string): string {
  if (platform === "win32") return "windows";
  return platform;
}

function archToArch(arch: string): string {
  if (arch === "x64") return "amd64";
  if (arch === "arm64") return "arm64";
  if (arch === "ia32") return "386";
  return arch;
}

function isLikelyGoBuild(name: string): boolean {
  return /-go\d{3}-/i.test(name);
}

function scoreAsset(name: string, arch: string): number {
  let score = 0;
  if (/compatible/i.test(name)) score -= 100;
  if (isLikelyGoBuild(name)) score -= 5;

  if (arch === "amd64") {
    if (/-amd64-v1-/i.test(name)) score += 60;
    else if (/-amd64-v2-/i.test(name)) score += 10;
    else if (/-amd64-v3-/i.test(name)) score += 0;
    else score += 20; // 没标 v1/v2/v3 的一般也可用
  }

  // 更短/更“通用”的名字轻微加分
  if (!/compatible/i.test(name) && !isLikelyGoBuild(name)) score += 2;
  return score;
}

function pickBestAsset(assets: GithubReleaseAsset[], os: string, arch: string): GithubReleaseAsset {
  const osPart = platformToOs(os);
  const archPart = arch;
  const prefix = `mihomo-${osPart}-${archPart}`;

  const wantExt = osPart === "windows" ? ".zip" : ".gz";
  const candidates = assets.filter((a) => a.name.startsWith(prefix) && a.name.endsWith(wantExt));
  if (candidates.length === 0) {
    throw new Error(`未找到适配当前系统的 mihomo 资源：prefix=${prefix} ext=${wantExt}`);
  }

  candidates.sort((a, b) => scoreAsset(b.name, archPart) - scoreAsset(a.name, archPart));
  return candidates[0];
}

async function findFileRecursive(dir: string, filename: string): Promise<string | null> {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const e of entries) {
    const p = join(dir, e.name);
    if (e.isFile() && e.name.toLowerCase() === filename.toLowerCase()) return p;
    if (e.isDirectory()) {
      const found = await findFileRecursive(p, filename);
      if (found) return found;
    }
  }
  return null;
}

export class MihomoInstaller {
  readonly dataDir: string;
  readonly storage: Storage;
  readonly repo: string;
  private installLock: Promise<MihomoInstallInfo> | null = null;

  constructor(dataDir: string, storage: Storage, repo = "MetaCubeX/mihomo") {
    this.dataDir = dataDir;
    this.storage = storage;
    this.repo = repo;
  }

  getSystem(): MihomoSystem {
    return {
      os: platformToOs(process.platform),
      arch: archToArch(process.arch)
    };
  }

  getBinPath(): string {
    const exe = platformToOs(process.platform) === "windows" ? "mihomo.exe" : "mihomo";
    return join(this.dataDir, "bin", exe);
  }

  getInstalled(): MihomoInstallInfo | null {
    const info = this.storage.getJson<MihomoInstallInfo>("mihomo_install");
    if (!info?.tag || !info?.assetName || !info?.binPath) return null;
    return info;
  }

  getStatus(): MihomoStatus {
    return {
      repo: this.repo,
      system: this.getSystem(),
      binPath: this.getBinPath(),
      installed: this.getInstalled()
    };
  }

  async fetchLatest(includePrerelease: boolean): Promise<GithubRelease> {
    const headers = {
      "user-agent": "mihomo-pool",
      "accept": "application/vnd.github+json"
    };

    if (!includePrerelease) {
      const url = `https://api.github.com/repos/${this.repo}/releases/latest`;
      const resp = await fetch(url, { headers });
      if (!resp.ok) throw new Error(`拉取 release 失败：HTTP ${resp.status}`);
      return (await resp.json()) as GithubRelease;
    }

    const url = `https://api.github.com/repos/${this.repo}/releases?per_page=20`;
    const resp = await fetch(url, { headers });
    if (!resp.ok) throw new Error(`拉取 releases 失败：HTTP ${resp.status}`);
    const list = (await resp.json()) as GithubRelease[];
    const first = list.find((r) => !((r as any).draft));
    if (!first) throw new Error("没有找到可用的 release");
    return first;
  }

  async getLatestInfo(includePrerelease: boolean): Promise<MihomoLatest> {
    const rel = await this.fetchLatest(includePrerelease);
    const sys = this.getSystem();
    const asset = pickBestAsset(rel.assets ?? [], sys.os, sys.arch);
    return {
      tag: rel.tag_name,
      prerelease: !!rel.prerelease,
      publishedAt: rel.published_at,
      assetName: asset.name,
      downloadUrl: asset.browser_download_url
    };
  }

  private async ensureBinDir(): Promise<string> {
    const dir = join(this.dataDir, "bin");
    await mkdir(dir, { recursive: true });
    return dir;
  }

  private async downloadToBuffer(url: string): Promise<Buffer> {
    const resp = await fetch(url, { headers: { "user-agent": "mihomo-pool" } });
    if (!resp.ok) throw new Error(`下载失败：HTTP ${resp.status}`);
    const ab = await resp.arrayBuffer();
    return Buffer.from(ab);
  }

  private async installFromGzip(buf: Buffer, targetPath: string): Promise<void> {
    const raw = gunzipSync(buf);
    await writeFile(targetPath, raw);
    if (platformToOs(process.platform) !== "windows") {
      await chmod(targetPath, 0o755);
    }
  }

  private async installFromZip(buf: Buffer, targetPath: string): Promise<void> {
    const tmpDir = join(this.dataDir, "bin", `tmp-${Date.now()}-${Math.random().toString(16).slice(2)}`);
    const tmpZip = join(tmpDir, "mihomo.zip");
    await mkdir(tmpDir, { recursive: true });
    await writeFile(tmpZip, buf);

    try {
      if (process.platform === "win32") {
        // Windows: 使用 PowerShell 解压
        const ps = Bun.spawn(
          [
            "powershell",
            "-NoProfile",
            "-Command",
            `Expand-Archive -Force -Path "${tmpZip}" -DestinationPath "${tmpDir}"`
          ],
          { stdout: "pipe", stderr: "pipe" }
        );
        const code = await ps.exited;
        if (code !== 0) {
          const err = await new Response(ps.stderr).text();
          throw new Error(`解压失败（PowerShell）：${err || `exit=${code}`}`);
        }
      } else {
        // macOS/Linux: 使用 unzip
        const uz = Bun.spawn(["unzip", "-o", tmpZip, "-d", tmpDir], { stdout: "pipe", stderr: "pipe" });
        const code = await uz.exited;
        if (code !== 0) {
          const err = await new Response(uz.stderr).text();
          throw new Error(`解压失败（unzip）：${err || `exit=${code}`}`);
        }
      }

      const wanted = platformToOs(process.platform) === "windows" ? "mihomo.exe" : "mihomo";
      const found = await findFileRecursive(tmpDir, wanted);
      if (!found) throw new Error(`解压后未找到 ${wanted}`);

      await rename(found, targetPath);
      if (platformToOs(process.platform) !== "windows") {
        await chmod(targetPath, 0o755);
      }
    } finally {
      try {
        await rm(tmpDir, { recursive: true, force: true });
      } catch {
        // ignore
      }
    }
  }

  async installLatest(includePrerelease: boolean, force = false): Promise<MihomoInstallInfo> {
    if (this.installLock) return await this.installLock;

    const task = (async () => {
      await this.ensureBinDir();

      const latest = await this.getLatestInfo(includePrerelease);
      const installed = this.getInstalled();
      if (!force && installed?.tag === latest.tag && existsSync(this.getBinPath())) {
        return installed;
      }

      const sys = this.getSystem();
      const buf = await this.downloadToBuffer(latest.downloadUrl);

      const binPath = this.getBinPath();
      const tmpPath = binPath + ".tmp";

      if (latest.assetName.endsWith(".gz")) {
        await this.installFromGzip(buf, tmpPath);
      } else if (latest.assetName.endsWith(".zip")) {
        await this.installFromZip(buf, tmpPath);
      } else {
        throw new Error(`不支持的资源格式：${latest.assetName}`);
      }

      // 原子替换
      try {
        await rename(binPath, binPath + ".bak");
      } catch {
        // ignore
      }
      await rename(tmpPath, binPath);

      const info: MihomoInstallInfo = {
        repo: this.repo,
        tag: latest.tag,
        assetName: latest.assetName,
        installedAt: nowIso(),
        binPath,
        system: sys
      };
      this.storage.setJson("mihomo_install", info);
      return info;
    })();

    this.installLock = task;
    try {
      return await task;
    } finally {
      this.installLock = null;
    }
  }
}
