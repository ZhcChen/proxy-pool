# 快速开始

## 依赖

- Bun（推荐使用你当前环境的 Bun 版本）
- 无需预装 `mihomo`：可在管理页「设置」里一键安装（从 GitHub Release 下载适配你系统的版本）

## 安装

在项目根目录执行：

```bash
bun install
```

## 启动

先设置管理员 Token（必填）：

```bash
export ADMIN_TOKEN='请替换为你的强随机 token'
```

```bash
bun run dev
```

默认管理页：`http://127.0.0.1:3320`

管理页登录仅需输入 `ADMIN_TOKEN`；浏览器会把该 Token 保存在 `localStorage`，刷新页面无需重新输入。

如需给外部系统读取实例池列表，可额外设置 `OPENAPI_TOKEN`（独立于 `ADMIN_TOKEN`）：

```bash
export OPENAPI_TOKEN='请替换为你的 openapi token'
```

然后通过 Bearer 鉴权调用：

```bash
curl -H "Authorization: Bearer <OPENAPI_TOKEN>" http://127.0.0.1:3320/openapi/pool
```

> 安全提示：当前版本为本地管理工具，请不要把管理端口暴露到公网，并妥善保管 `ADMIN_TOKEN`（以及启用时的 `OPENAPI_TOKEN`）。
