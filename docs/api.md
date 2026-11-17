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
