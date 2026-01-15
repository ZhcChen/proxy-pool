import { mkdir } from "node:fs/promises";
import { join, normalize, resolve } from "node:path";
import net from "node:net";

export function nowIso(): string {
  return new Date().toISOString();
}

export function resolveUnder(rootDir: string, requestPath: string): string | null {
  const safePath = normalize(requestPath).replace(/^(\.\.(\/|\\|$))+/, "");
  const fullPath = resolve(join(rootDir, "." + safePath));
  const fullRoot = resolve(rootDir);
  if (!fullPath.startsWith(fullRoot)) return null;
  return fullPath;
}

export async function ensureDir(path: string): Promise<void> {
  await mkdir(path, { recursive: true });
}

export async function isPortFree(port: number, host = "127.0.0.1"): Promise<boolean> {
  return await new Promise<boolean>((resolve) => {
    const server = net.createServer()
      .once("error", () => resolve(false))
      .once("listening", () => server.close(() => resolve(true)));
    server.listen(port, host);
  });
}

export async function findNextFreePort(startPort: number, host = "127.0.0.1"): Promise<number> {
  let port = startPort;
  for (let i = 0; i < 2000; i++) {
    if (await isPortFree(port, host)) return port;
    port++;
  }
  throw new Error("找不到可用端口（扫描范围过大或端口被占用）");
}

export async function findNextFreePortAvoiding(
  startPort: number,
  usedPorts: ReadonlySet<number>,
  host = "127.0.0.1"
): Promise<number> {
  let port = startPort;
  for (let i = 0; i < 2000; i++) {
    if (usedPorts.has(port)) {
      port++;
      continue;
    }
    if (await isPortFree(port, host)) return port;
    port++;
  }
  throw new Error("找不到可用端口（扫描范围过大或端口被占用）");
}
