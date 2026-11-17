# MCP 接入范围

**当前 `cmd/certcenter` 不提供 MCP 端点。** 对 CertCenter 发送 `POST /mcp` 返回 `404`；没有可调用的证书签发、续期或部署 MCP 工具。后台自动续期是任务调度器功能，见[自动化与运行](/docs/operations)，并非 MCP。

## 仓库中保留的 ConfigCenter MCP

`internal/server/mcp.go` 和 `api_operations.go` 保留了 ConfigCenter 的管理协议实现。它仅在服务器未配置 `Remote` 的旧配置管理分支挂载；CertCenter 的启动流程会配置 `Remote`，包括使用本地 KV 时。切换到本地 KV 不会启用 MCP，也没有启用它的 CertCenter 环境变量。

以下内容描述仓库保留的实现，适用于实际挂载该实现的 ConfigCenter 服务。连接地址必须是该服务的 `/mcp`，不能使用 CertCenter 地址代替。工具作用于项目配置、环境、API key、提案、用户和设置，不操作 CertCenter 证书任务。

## 传输与认证

实现接受 HTTP POST，消息为单个 JSON-RPC 2.0 对象，`Content-Type: application/json`。协议版本为 `2025-06-18`；提供 `MCP-Protocol-Version` 时必须与其一致。响应是 JSON，不建立 SSE 流或有状态 MCP 会话；GET 和 DELETE 返回 `405`。

使用 ConfigCenter API key 时发送 `Authorization: ApiKey cc_…`。浏览器会话也可认证，但 POST 必须带会话的 `X-CSRF-Token`。若请求带 `Origin`，必须与服务配置的公开来源一致。发现结果按权限过滤，实际调用仍执行 API 权限和项目隔离检查。

## 初始化、发现和调用

按顺序向同一端点发送下列请求。示例是请求体；认证头需由客户端提供。

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": { "name": "config-client", "version": "1.0.0" }
  }
}
```

初始化返回 `serverInfo.name: "configcenter"` 和 tools 能力。客户端发送 `notifications/initialized` 通知（不带 `id`，服务返回 `202`），随后发现工具：

```json
{ "jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {} }
```

调用配置读取工具的示例，需要 `config:read` 且有目标项目权限：

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "get_config",
    "arguments": {
      "project": "certcenter",
      "environment": "production",
      "key": "certcenter.providers"
    }
  }
}
```

`get_config` 会返回配置值，可能包含凭据；仅在获授权的客户端使用。`set_config` 立即写入，`propose_config` 创建待审核提案，`review_proposal` 执行审核。是否可见、参数类型、必填项和枚举以 `tools/list` 的 `inputSchema` 为准。不要把 HTTP 路径或旧示例中的名称当成工具名称。

实现还支持 `ping`；`resources/list`、`prompts/list` 返回空列表。无效参数返回 JSON-RPC `-32602`，未知方法返回 `-32601`；API 执行失败则返回工具结果 `isError: true`，并附 HTTP 状态和响应体。

## CertCenter 当前可用的自动化方式

- 用 [HTTP API](/docs/api) 和 OAuth 会话发起签发、部署和管理操作，写操作带 CSRF token。
- 用 [Go SDK](/docs/sdk) 维护远端 ConfigCenter 或本地 KV 中的策略，由常驻 CertCenter 调度器读取执行。
- 在证书策略中启用自动续期、自动部署，运行约束见[自动化与运行](/docs/operations)。
