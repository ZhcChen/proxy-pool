# 目录结构

当前核心目录如下：

- `api/`：后端服务（Go + Gin）
- `api/src/web/public/`：管理页静态资源（编译时 embed 到二进制）
- `data/`：运行时数据（SQLite、实例配置、订阅 YAML）
- `docs/`：需求、方案与 OpenAPI 文档
- `tests/go/`：Go 测试
