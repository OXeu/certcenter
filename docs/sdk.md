# ConfigCenter 配置指南

CertCenter 使用 `github.com/OXeu/configcenter/sdk/configcenter`。仓库保留 SDK 独立模块，新增 `Put(ctx, key, value)`：先从 `/api/v1/agent/project` 获取 API key 绑定的项目，再调用现有配置写入 API。服务运行需要单项目 key 的 `projects:read`、`environments:read`、`config:read`、`config:write` 权限。

本地仅配置 `CC_CONFIGCENTER_URL`、`CC_CONFIGCENTER_API_KEY`、`CC_CONFIGCENTER_ENVIRONMENT` 和可选的 `CC_ENV_FILE`；其他环境变量不作为 CertCenter 业务配置来源。

## certcenter.server

在 ConfigCenter 中创建此名称的配置项，值为 JSON：

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
