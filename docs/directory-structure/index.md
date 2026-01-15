# 目录结构

## workspaces

- `api/`：后端服务（Bun 运行时）
- `web/`：管理页（当前为静态资源目录 `web/public/`，由 `api` 直接托管）

## 数据目录

默认 `data/`：

- `data/state.sqlite`：持久化状态（订阅、实例、设置）
- `data/subscriptions/*.yaml`：缓存的订阅内容
- `data/instances/<id>/config.yaml`：每个实例生成的 mihomo 配置
