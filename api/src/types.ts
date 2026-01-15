export type Subscription = {
  id: string;
  name: string;
  url: string | null;
  createdAt: string;
  updatedAt: string;
  lastError: string | null;
  proxies: MihomoProxy[];
};

export type Instance = {
  id: string;
  name: string;
  subscriptionId: string;
  proxyName: string;
  proxy: MihomoProxy;
  mixedPort: number;
  controllerPort: number;
  autoStart: boolean;
  autoSwitch: boolean;
  createdAt: string;
  updatedAt: string;
};

export type ProxyAuth = {
  enabled: boolean;
  username: string;
  password: string;
};

export type Settings = {
  bindAddress: string;
  allowLan: boolean;
  logLevel: "silent" | "error" | "warning" | "info" | "debug";
  baseMixedPort: number;
  baseControllerPort: number;
  maxLogLines: number;
  healthCheckIntervalSec: number;
  healthCheckUrl: string;
  proxyAuth: ProxyAuth;
};

export type State = {
  version: 1;
  settings: Settings;
  subscriptions: Subscription[];
  instances: Instance[];
};

export type MihomoProxy = Record<string, unknown> & {
  name: string;
  type?: string;
};
