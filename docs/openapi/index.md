# OpenAPI（纯 MD）

当前对外接口仅开放实例池只读查询。

## 鉴权方式

- 请求头：`Authorization: Bearer <OPENAPI_TOKEN>`
- Token 来源：服务端环境变量 `OPENAPI_TOKEN`
- `OPENAPI_TOKEN` 与管理端 `ADMIN_TOKEN` 独立，不互通

## 1. 获取实例池列表

- 方法：`GET`
- 路径：`/openapi/pool`
- 描述：返回当前实例池中每个实例的代理导出信息（一个实例一个 `host:port`）

### 请求示例

```bash
curl -H "Authorization: Bearer <OPENAPI_TOKEN>" \
  http://127.0.0.1:3320/openapi/pool
```

### 成功响应（200）

```json
{
  "ok": true,
  "proxies": [
    {
      "id": "c3a8a2a4-0d8f-4f0e-b4a6-5a2d6df6f18a",
      "name": "SKYLUMO / 🇯🇵 日本-东京-01",
      "mixedPort": 30001,
      "proxy": "203.0.113.10:30001",
      "running": true
    }
  ]
}
```

### 字段说明

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `ok` | `boolean` | 是否成功 |
| `proxies` | `array` | 实例池列表 |
| `proxies[].id` | `string` | 实例 ID |
| `proxies[].name` | `string` | 实例名称（通常为“订阅名 / 节点名”） |
| `proxies[].mixedPort` | `number` | 代理端口 |
| `proxies[].proxy` | `string` | 导出地址，格式 `host:port`（IPv6 会自动带 `[]`） |
| `proxies[].running` | `boolean` | 实例是否运行中 |

### 错误响应

1. `401 unauthorized`（缺少或错误的 Bearer Token）

```json
{
  "ok": false,
  "error": "unauthorized"
}
```

2. `503 openapi disabled`（服务端未设置 `OPENAPI_TOKEN`）

```json
{
  "ok": false,
  "error": "openapi disabled: missing OPENAPI_TOKEN"
}
```
