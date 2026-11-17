# CertCenter HTTP API

管理 API 使用 OAuth 浏览器会话。所有非 GET 操作必须提交会话的 `X-CSRF-Token`。当前不提供证书私钥下载接口或独立的 CertCenter API key。

| 方法   | 路径                                                  | 用途                                                       |
| ------ | ----------------------------------------------------- | ---------------------------------------------------------- |
| GET    | `/api/v1/domains`                                     | 当前用户可见的域名                                         |
| POST   | `/api/v1/domains`                                     | 管理员添加域名，`name`、`owner`、`certificates`            |
| GET    | `/api/v1/domains/:domain`                             | 策略、版本、证书有效期、任务、最近 500 条日志和 CNAME 提示 |
| PUT    | `/api/v1/domains/:domain`                             | 保存 `version` 和完整 `certificates` 策略列表              |
| POST   | `/api/v1/domains/:domain/certificates/:policy/issue`  | 排队签发/续签                                              |
| POST   | `/api/v1/domains/:domain/certificates/:policy/deploy` | 排队部署最新有效证书                                       |
| GET    | `/api/v1/certificate-settings`                        | 管理员读取不含凭据的设置摘要                               |
| GET    | `/api/v1/users`                                       | 管理员列出用户                                             |
| PATCH  | `/api/v1/users/:id`                                   | 管理员修改用户角色                                         |
| DELETE | `/api/v1/users/:id`                                   | 管理员删除其他用户                                         |

## 保存策略

先获取域名详情中的 `version`，再提交：

```json
{
  "version": "GET 返回的版本标识",
  "certificates": [
    {
      "id": "wildcard",
      "names": ["example.com", "*.example.com"],
      "dns_provider": "aliyun-dns",
      "enabled": true,
      "auto_renew": true,
      "renew_before_days": 30,
      "auto_deploy": true,
      "upstreams": ["tencent-ssl", "aliyun-ssl"]
    }
  ]
}
```

只允许域名本身、子域名及其通配符；ID 必须唯一。配置保存通过 ConfigCenter SDK 完成，证书材料与日志不混入配置项。

签发和部署返回 `202` 及任务 ID。重复任务或过期配置版本返回 `409`；越权域名返回 `404`；CSRF 校验失败返回 `403`；ConfigCenter 或数据库不可用返回 `503`。前端配置版本检查可防止同一管理实例中的过期覆盖，不能替代 ConfigCenter 的跨实例比较交换。

原项目的配置项管理 API、API key 管理 API 和 `/mcp` 在 CertCenter 运行模式中不挂载。

## DNS 服务商与部署上游

这两组接口仅管理员可用，使用同一会话及 CSRF 规则：

| 方法   | 路径                          | 用途                        |
| ------ | ----------------------------- | --------------------------- |
| GET    | `/api/v1/dns-providers`       | DNS 账号列表及配置版本      |
| POST   | `/api/v1/dns-providers`       | 添加 DNS 账号               |
| PUT    | `/api/v1/dns-providers/:name` | 编辑 DNS 账号               |
| DELETE | `/api/v1/dns-providers/:name` | 删除未被策略引用的 DNS 账号 |
| GET    | `/api/v1/upstreams`           | 上游列表及配置版本          |
| POST   | `/api/v1/upstreams`           | 添加部署上游                |
| PUT    | `/api/v1/upstreams/:name`     | 编辑部署上游                |
| DELETE | `/api/v1/upstreams/:name`     | 删除未被策略引用的上游      |

GET 返回 `{ "version": "…", "providers": [{ "name": "…", "fields": { "kind": "…" } }] }`。`fields` 仅包含非敏感字段：类型、地域，以及 DNS 服务地址或 SSH 地址、用户、主机公钥、文件路径和重载命令。不返回访问密钥 ID、密钥 Secret、SSH 私钥或 acme-dns 注册凭据。

创建 DNS 账号的请求体：

```json
{
  "version": "列表 GET 返回的版本",
  "name": "aliyun-dns",
  "dns": {
    "kind": "alidns",
    "access_key": "填写账号的访问密钥 ID",
    "secret_key": "填写账号的访问密钥 Secret",
    "region": "cn-hangzhou"
  }
}
```

创建上游使用相同结构，把 `dns` 换为 `upstream`。DNS 类型为 `alidns`、`tencentcloud`、`acme-dns`；上游类型为 `aliyun`、`tencentcloud`、`ssh`。各类型字段见[配置指南](/docs/config)。创建成功返回 `204`，新账号随即出现在域名详情的 `dns_providers` / `upstreams` 中。

编辑使用 PUT，账号名称来自路径且类型不可变；提交完整的非敏感字段。`access_key`、`secret_key` 和 `private_key` 留空或省略表示保留原值。acme-dns 的 `accounts` 省略或为 `null` 表示保留，提供对象则整体替换，包括所有注册凭据。修改已有账号类型应新建账号后迁移策略。

删除请求体为 `{ "version": "列表 GET 返回的版本" }`。删除及编辑成功返回 `204`；缺失凭据等验证错误返回 `400`，同名创建、版本过期或破坏策略引用返回 `409`。版本覆盖整个 provider 配置，另一类账号的修改也会使旧版本失效；它只能保护当前管理实例中的写入，外部 ConfigCenter 写入仍需避免并发覆盖。
