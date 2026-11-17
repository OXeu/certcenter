# CertCenter 配置指南

CertCenter 使用 `github.com/OXeu/configcenter/sdk/configcenter`。仓库保留 SDK 独立模块，新增 `Put(ctx, key, value)`：先从 `/api/v1/agent/project` 获取 API key 绑定的项目，再调用现有配置写入 API。服务运行需要单项目 key 的 `projects:read`、`environments:read`、`config:read`、`config:write` 权限。

本地仅配置 `CC_CONFIGCENTER_URL`、`CC_CONFIGCENTER_API_KEY`、`CC_CONFIGCENTER_ENVIRONMENT`、`CC_CONFIGCENTER_LOCAL_PATH` 和可选的 `CC_ENV_FILE`；其他环境变量不作为 CertCenter 业务配置来源。

## 首次启动自动初始化

每次启动通过 `EnsureDefaults` 检查配置，自动创建缺失的 `certcenter.server`、`certcenter.providers` 两个配置项。服务、数据库、OAuth、用户组和 ACME 等固定设置的默认值保存在这两个 JSON 中；DNS 和部署账号列表默认为空，不生成示例账号。既有配置项（包括空值或错误值）保持原样，避免替换管理员配置和加密密钥。

远端通过批量读取接口检查键，再用 `PUT /api/v1/projects/:project/environments/:env/items` 和 `config:write` 批量补齐缺失项，不再提供独立初始化 API。首次初始化应由一个实例完成，批量读写之间不提供跨请求并发保护。本地仍通过 bbolt 事务原子初始化，已有值不会覆盖。缺少权限或批量接口时会报出相关 key 和 HTTP 状态。

SDK 提供 `ReadMany(ctx, keys)` 和 `PutMany(ctx, values)`，分别使用一次请求读写多个配置。`ReadMany` 返回键值映射，省略缺失键，不替换已加载快照；`PutMany` 使用一个事务创建或更新整批值并更新快照。每批 1–500 个键，远端写入请求体最多 1 MiB。原有 `Put` 和单项读取保持兼容。

例如缺失项读取错误会显示 `key "certcenter.server": configuration not found`；无效配置会显示 `validate key "certcenter.server": ...`，不输出配置值或凭据。初始化完成后仍需填写实际 OAuth、DNS 和部署凭据；默认不接受 ACME 条款，不自动执行云端操作。

## 不配置远端：本地 KV 模式

URL 和 API key 同时为空时，SDK 使用 [bbolt](https://github.com/etcd-io/bbolt)，默认文件是 `.tmp/certcenter-config.kv`，按环境隔离，文件权限为 `0600`。每次操作以事务完成并关闭文件；缺失配置在同一事务中初始化，已有值不会被默认值覆盖。CertCenter 首次启动生成并保存加密密钥，以后重启复用它。

基础配置初始化后，使用同一 SDK 编辑 `certcenter.server`、`certcenter.providers` 等键。OAuth 登录可用后，管理员也可直接在“DNS 服务商”和“部署上游”页面添加、编辑和删除账号。以下示例函数供调用方程序使用（程序应在项目根目录运行，或传入相同的绝对文件路径）：

```go
func saveLocalSettings(ctx context.Context, key, jsonValue string) error {
    client, err := configcenter.New(configcenter.Config{
        LocalPath: ".tmp/certcenter-config.kv",
        Environment: "production",
    })
    if err != nil { return fmt.Errorf("create local settings client: %w", err) }
    if err := client.Put(ctx, key, jsonValue); err != nil {
        return fmt.Errorf("save local settings: %w", err)
    }
    return nil
}
```

导入 `context`、`fmt` 和 `github.com/OXeu/configcenter/sdk/configcenter`。可先调用 `Load(ctx)` 和 `Get("certcenter.server")` 读取现有 JSON，再修改 OAuth 等字段，保留已有 `encryption_key`。初始没有 OAuth 登录、DNS 或云凭据；补齐后才能登录和签发。域名与策略的页面保存操作也通过同一 SDK 写入本地 KV。

仅填写 URL 或 API key 会报错。远端已经配置时，服务错误不会触发本地兜底；`LocalPath` 和 `LocalDefaults` 被忽略。切换本地/远端不会自动迁移配置；迁移时必须保留加密密钥，且本地文件应与证书数据库一起备份。

## certcenter.server

启动时自动创建此配置项；可在 ConfigCenter 中编辑。以下 JSON 展示可填写的字段：

```json
{
  "addr": "127.0.0.1:8081",
  "public_url": "https://cert.example.com",
  "database_driver": "sqlite",
  "database_dsn": "file:.tmp/certcenter.db",
  "encryption_key": "替换为随机的32字节密钥的base64编码",
  "languages": ["zh-CN", "en"],
  "allow_registration": false,
  "bootstrap": { "admin_email": "admin@example.com", "admin_name": "Admin" },
  "oauth": {
    "issuer_url": "https://id.example.com",
    "client_id": "certcenter",
    "client_secret": "替换为OAuth密钥",
    "scopes": ["openid", "profile", "email", "groups"],
    "groups_claim": "groups"
  },
  "groups": { "admin_groups": ["certcenter-admin"], "mapping": {} }
}
```

使用 `openssl rand -base64 32` 生成加密密钥，并安全保存到 ConfigCenter。不得替换已使用的加密密钥，除非同时实施数据重新加密。OAuth 回调默认为 `public_url + /oauth/callback`；可用 `oauth.redirect_url` 覆盖。管理员邮箱须与身份提供者返回的邮箱一致。会话默认 24 小时，可用 `session_ttl` 指定 Go duration 的纳秒数。

MySQL 示例：`database_driver` 为 `mysql`，`database_dsn` 为 `user:password@tcp(host:3306)/certcenter?charset=utf8mb4`，程序自动启用 `parseTime`。生产数据库及加密密钥需要备份。SQLite 默认路径仅用于开发。

## certcenter.providers

```json
{
  "acme": {
    "directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
    "email": "acme@example.com",
    "accept_terms": true
  },
  "dns": {
    "aliyun-dns": {
      "kind": "alidns",
      "access_key": "ID",
      "secret_key": "SECRET",
      "region": "cn-hangzhou"
    },
    "tencent-dns": { "kind": "tencentcloud", "access_key": "ID", "secret_key": "SECRET" },
    "cloudflare-dns": { "kind": "cloudflare", "api_token": "填写限定域名权限的 API Token" },
    "delegated": {
      "kind": "acme-dns",
      "api_base": "https://auth.example.net",
      "accounts": {
        "example.com": {
          "username": "注册返回的username",
          "password": "注册返回的password",
          "subdomain": "注册返回的subdomain",
          "fulldomain": "registration.auth.example.net"
        }
      }
    }
  },
  "upstreams": {
    "tencent-ssl": { "kind": "tencentcloud", "access_key": "ID", "secret_key": "SECRET" },
    "aliyun-ssl": {
      "kind": "aliyun",
      "access_key": "ID",
      "secret_key": "SECRET",
      "region": "cn-hangzhou"
    },
    "nginx": {
      "kind": "ssh",
      "address": "192.0.2.10:22",
      "user": "deploy",
      "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----\n",
      "host_key": "ssh-ed25519 AAAA...",
      "certificate_path": "/etc/nginx/tls/example.com/fullchain.pem",
      "key_path": "/etc/nginx/tls/example.com/privkey.pem",
      "reload_command": "sudo nginx -t && sudo systemctl reload nginx"
    }
  }
}
```

删除不使用的 provider 条目；每个配置条目都需要有效的必填字段。DNS provider 和 upstream 分别通过独立接口扩展。腾讯云账号需要 DNSPod TXT 写入/删除或 SSL `UploadCertificate` 对应权限；阿里云需要 AliDNS TXT 权限或 `yundun-cert:UploadUserCertificate`。DNS 与部署凭据分开配置，可使用不同的权限受限账号。

`accept_terms` 表示运营者已接受所选 CA 的条款；未接受时签发失败，不自动替运营者接受。初次测试请使用 staging；其证书不受浏览器信任。确认后改用生产目录 `https://acme-v02.api.letsencrypt.org/directory` 并重新签发。不同 CA/邮箱有独立且持久的加密 ACME 账户密钥。

### Cloudflare DNS

在“DNS 服务商”页面选择 Cloudflare，填写 API Token。Token 需要管理目标区域的 `Zone / Zone / Read` 和 `Zone / DNS / Edit` 权限；不需要账号邮箱或 Global API Key。签发时创建 TXT，完成后按该次创建的记录 ID 删除。编辑时 Token 留空保留原值，不向浏览器回传已有 Token。权限依据：[lego Cloudflare 文档](https://go-acme.github.io/lego/dns/cloudflare/index.html)。

### acme-dns 委派

部署兼容 [acme-dns](https://github.com/acme-dns/acme-dns) 的权威 DNS 服务，向它的 `/register` 注册，保存返回数据到 `accounts`。把 `_acme-challenge.example.com` CNAME 指向返回的 `fulldomain`。`example.com` 与 `*.example.com` 共享注册记录；另一个具体子域名需要自己的注册与 CNAME。

CertCenter 通过带 `X-Api-User` / `X-Api-Key` 的 `/update` 发布验证 TXT，兼容服务滚动保留两条 TXT，支持根域名与通配符同时验证。账户配置经 SDK 读取，不使用本地 acme-dns JSON 文件。

## certcenter.domain.example.com

管理界面自动创建和维护每个域名的独立 JSON 配置项：

```json
{
  "name": "example.com",
  "owner": "admin@example.com",
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

域名使用小写 ASCII 或 punycode。所有者由管理员创建域名时指定，转移所有者可直接编辑 ConfigCenter 中的 `owner`。停用策略用 `enabled: false`，已签发历史和日志仍保留。外部编辑与页面编辑同一配置项时请避免并发覆盖。

若完整配置项名称超过 ConfigCenter 的 191 字符限制，管理界面自动使用 `certcenter.domain.sha256-<域名SHA-256>` 作为键，JSON 中仍保留完整域名。

接口依据：[lego DNS providers](https://go-acme.github.io/lego/dns/)、[腾讯云上传证书](https://cloud.tencent.com/document/api/400/41665)、[阿里云 UploadUserCertificate](https://www.alibabacloud.com/help/en/ssl-certificate/developer-reference/api-cas-2020-04-07-uploadusercertificate)。
