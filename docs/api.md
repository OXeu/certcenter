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
| GET    | `/api/v1/certificate-settings`                        | 管理员读取可编辑设置、版本与待重启状态（不含凭据）         |
| PUT    | `/api/v1/certificate-settings/:section`               | 管理员按分组保存设置                                       |
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

创建上游使用相同结构，把 `dns` 换为 `upstream`。DNS 类型为 `alidns`、`tencentcloud`、`cloudflare`、`acme-dns`；上游类型为 `aliyun`、`tencentcloud`、`ssh`。各类型字段见[配置指南](/docs/config)。创建成功返回 `204`，新账号随即出现在域名详情的 `dns_providers` / `upstreams` 中。

编辑使用 PUT，账号名称来自路径且类型不可变；提交完整的非敏感字段。`access_key`、`secret_key`、Cloudflare 的 `api_token` 和 `private_key` 留空或省略表示保留原值。内置 acme-dns 无需凭据，API 不接受 `api_base` 或 `accounts` 注册信息。已有外部 acme-dns 配置编辑时保留其原配置。`certcenter-acme-dns` 由服务器配置管理，不能通过账号管理 API 修改或删除。修改已有账号类型应新建账号后迁移策略。

删除请求体为 `{ "version": "列表 GET 返回的版本" }`。删除及编辑成功返回 `204`；缺失凭据等验证错误返回 `400`，同名创建、版本过期或破坏策略引用返回 `409`。版本覆盖整个 provider 配置，另一类账号的修改也会使旧版本失效；它只能保护当前管理实例中的写入，外部 ConfigCenter 写入仍需避免并发覆盖。

## CNAME 委派验证

启用内置 DNS 后，域名详情的 `dns_providers` 包含 `certcenter-acme-dns`；`delegations` 返回系统分配的 `{ "name": "_acme-challenge.example.com", "target": "随机标识.auth.example.com" }`。没有策略的新域名也会获得根域名记录。策略包含子域名时，在保存后自动分配记录，根域名和通配符去重。详情仅向管理员和域名所有者开放。

`POST /api/v1/domains/:domain/delegations/check` 使用会话与 CSRF，返回 `{ "delegations": [{ "name": "…", "target": "…", "verified": true }] }`。检查目标来自服务端，不接受客户端指定的地址；结果反映当前解析状态，不替代 CA 的 DNS-01 验证。

Cloudflare 账号的创建请求中 `dns` 为 `{ "kind": "cloudflare", "api_token": "…" }`，已有 Token 不会通过列表接口返回。DNS 列表额外返回 `delegation_zone`，为空表示尚未启用内置 DNS。

## 修改服务设置

设置页使用 `GET /api/v1/certificate-settings` 读取 `general`、`acme`、`dns`、`oauth`、`access`、`database` 六组设置。响应同时给出 `server_version`、`providers_version` 和 `restart_required`，并保留原有摘要字段。OAuth 密钥、数据库连接字符串、会话密钥和加密密钥不会出现在响应中；只返回是否已配置的标志。

管理员使用 `PUT /api/v1/certificate-settings/:section` 保存一组配置，所有写操作都需要会话 CSRF token。`acme` 使用 `providers_version`，其他分组使用 `server_version`：

```json
{
  "version": "GET 返回的 providers_version",
  "values": {
    "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
    "email": "acme@example.com",
    "accept_terms": true
  }
}
```

| section    | values 字段                                                                                 | 生效方式               |
| ---------- | ------------------------------------------------------------------------------------------- | ---------------------- |
| `general`  | `addr`、`public_url`、`session_ttl`（如 `24h`）、`languages`                                | 重启                   |
| `acme`     | `directory_url`、`email`、`accept_terms`                                                    | 后续证书任务           |
| `dns`      | `zone`、`listen_address`、`nameserver`、`addresses`                                         | 重启                   |
| `oauth`    | `issuer_url`、`client_id`、`client_secret`、`scopes`、`redirect_url`、`groups_claim`        | 重启                   |
| `access`   | `allow_registration`、`admin_groups`、`mapping`、`bootstrap`（`admin_email`、`admin_name`） | 注册开关即时，其余重启 |
| `database` | `driver`、`dsn`                                                                             | 重启；不迁移数据       |

省略的字段保留原值；OAuth `client_secret` 和数据库 `dsn` 留空也保留原值。数组及用户组 `mapping` 显式提交时替换整个对应字段，映射角色仅允许 `admin`、`user`。`languages` 至少包含一个支持的语言。存储中的 `session_ttl` 仍为纳秒整数，页面 API 用时间长度字符串。

保存成功返回 `200` 及更新后的完整设置视图，不重新连接数据库、调用 OAuth 服务或重启 DNS 监听。返回的 `restart_required` 与当前运行配置比较；进程重启后使用已保存配置。未知分组返回 `404`，不合法字段或配置返回 `400`，过期版本返回 `409`，存储写入失败返回 `503`。检查和保存受当前管理实例的互斥锁保护，外部 ConfigCenter 写入或其他实例仍需避免并发覆盖。

设置写入只修改所选分组，并保留未编辑分组、DNS/上游凭据、已有加密密钥及未知扩展字段。页面不会接受加密密钥轮换参数。
