# CertCenter

证书签发、续期与部署管理平台，从 ConfigCenter 复制，保留 Go / Gin / GORM、OAuth 登录、Vue 3 和共享对话框。

- **域名工作区**：按所有者隔离，展示证书策略、SAN、有效期、历史版本、签发和部署日志。
- **ACME**：使用 lego；DNS API 支持腾讯云 DNSPod、阿里云 DNS；另支持 acme-dns 的 `_acme-challenge` CNAME 委派。
- **部署**：腾讯云 SSL 和阿里云 CAS 证书库上传；SSH/SFTP 安装证书、私钥并执行可选的重载命令。
- **自动化**：持久化任务队列、任务互斥、过期租约恢复、每分钟检查续期、失败后一小时重试、成功上传回执去重。短生命周期证书的续期窗口不超过证书有效期的三分之一。
- **存储**：统一 `certificates.Repository` 接口，GORM 适配 SQLite / MySQL。证书历史、私钥、ACME 账户和日志保存在数据库；密钥材料以 AES-256-GCM 加密。
- **配置**：服务设置、DNS 凭据、上游、域名及续签策略均通过 ConfigCenter Go SDK 读写。未配置远端时使用 bbolt 本地 KV；环境变量仅用于 SDK 连接和本地文件路径。

## 启动

需要 Go 1.26.10、Node.js 24+、npm 和 golangci-lint v2。未填写远端地址和 API key 时，可以直接构建启动；SDK 在 `.tmp/certcenter-config.kv` 原子初始化基础配置和持久加密密钥。初始 OAuth、DNS 和部署凭据为空，ACME 条款未接受，需要按[配置指南](docs/sdk.md)通过 SDK 补齐后使用。可用 `CC_CONFIGCENTER_LOCAL_PATH` 指定持久文件路径。

如使用远端 ConfigCenter，创建专用项目和 `production` 环境，为单项目 API key 授予 `projects:read`、`environments:read`、`config:read`、`config:write`，再执行以下配置步骤。

1. 使用支持默认值初始化接口的新版 ConfigCenter。首次启动会自动创建缺失的 `certcenter.server` 和 `certcenter.providers`，无需手工创建空配置项。
2. 创建本机 `.env.local`，只填连接引导配置：

   ```dotenv
   CC_CONFIGCENTER_URL=https://config.example.com
   CC_CONFIGCENTER_API_KEY=cc_your_project_scoped_key
   CC_CONFIGCENTER_ENVIRONMENT=production
   ```

   可通过 `CC_ENV_FILE` 指定另一个文件。已导出的变量优先，凭据不写入版本库。

3. 构建并启动：

   ```sh
   npm ci
   make check
   make build
   ./.tmp/bin/certcenter
   ```

4. 默认地址为 `127.0.0.1:8081`。首次启动后按[配置指南](docs/sdk.md)编辑自动生成的配置，填入 OAuth、DNS、部署凭据，保留生成的加密密钥，并重启服务。使用 OAuth 登录后，管理员在“域名”页添加域名和所有者；进入域名后添加证书策略，再执行签发或启用自动续签。

生产环境应运行常驻进程，并使用持久数据库。原项目的 SCF 自动部署已移除，避免请求冻结期间续期任务停止，也避免部署到原项目服务。副本保留原 Git 历史，但移除了指向 ConfigCenter 的远端。

默认 SQLite 数据库位于 `.tmp/certcenter.db`，仅用于本地运行；生产可在远端配置中指定持久 SQLite 路径或 MySQL。备份数据库及 ConfigCenter 中的 `encryption_key`，直接替换该密钥会使已有证书和 ACME 账户无法解密；当前未实现密钥轮换迁移。

## 使用边界

- 云上传返回云端证书 ID，首期不自动绑定 CDN、CLB、WAF 等云产品；SSH 可在上传后运行管理员配置的重载命令。
- acme-dns 使用已部署的兼容服务，需先注册记录并设置 CNAME；本项目不内置权威 DNS 服务器。
- 服务地址、数据库、OAuth、加密密钥等启动设置修改后重启生效；域名策略、服务商配置和注册开关实时通过 SDK 读取。远端配置不完整或请求失败不会切换到本地。切换存储方式时需要自行迁移设置，并保持原加密密钥。
- 配置写入使用 SDK 中新增的 `Put`，兼容原 ConfigCenter 管理 API。ConfigCenter 暂无跨实例 CAS；证书任务可共享数据库运行，管理界面的配置写入建议只由一个实例承接，并避免同时在 ConfigCenter 编辑同一域名记录。
- SSH 要求固定主机公钥、可写的绝对路径和支持 `posix-rename` 的 SFTP 服务。每个文件原子替换，两个文件不构成跨文件事务；写入或重载失败会留下失败日志，可重试。为不同证书策略配置独立目标路径。

## 开发与验证

```sh
make check
make build
npm run dev
```

Vite 开发服务代理至 `127.0.0.1:8081`。构建输出在 `.tmp/ui`、`.tmp/docs`、`.tmp/bin`。测试使用内存数据库和 `.tmp/` 下的临时文件，覆盖域名隔离、CSRF、策略校验、签发失败保留、自动续签、部分上传重试、任务恢复、加密、云 SDK 请求和 SSH/SFTP。

源码边界：`internal/certconfig` 配置模型和 SDK 适配；`internal/dnsprovider` DNS 抽象；`internal/upstream` 部署抽象；`internal/certificates` 签发与调度；`internal/store` 数据库模型和查询；`internal/server` 会话与 HTTP；`web/ui/src` 界面。

[使用指南](docs/index.md) · [HTTP API](docs/api.md) · [ConfigCenter 配置](docs/sdk.md) · [自动化说明](docs/mcp.md)

## 腾讯云函数部署

`serverless.yml` 将应用部署为广州区的 `certcenter` Web 函数，监听地址需在 ConfigCenter 的 `certcenter.server` 中配置为 `0.0.0.0:9000`。数据库使用有持久存储的 MySQL；函数本地 KV 和 SQLite 不用于生产部署。

GitHub Actions 的 `production` 部署使用以下仓库配置：

| 类型 | 名称 | 内容 |
| --- | --- | --- |
| Secret | `TENCENT_SECRET_ID`、`TENCENT_SECRET_KEY` | 腾讯云部署凭据 |
| Secret | `SCF_RUNTIME_ENV` | SDK 连接的 dotenv 文件，见下方示例 |
| Variable | `SCF_REGION`、`SCF_VPC_ID`、`SCF_SUBNET_ID` | 函数地域和数据库所在网络 |
| Variable | `SCF_PUBLIC_URL` | 部署后验证使用的 HTTPS 地址 |
| Variable | `SCF_DEPLOY_ENABLED` | 设置为 `true` 后，main 分支检查通过会自动部署 |

```dotenv
CC_CONFIGCENTER_URL=https://your-configcenter.example
CC_CONFIGCENTER_API_KEY=cc_replace
CC_CONFIGCENTER_ENVIRONMENT=production
```

MySQL、OAuth、会话密钥和公网地址等业务设置通过 SDK 写入 ConfigCenter 的生产环境。数据库须提前创建，并授予运行账号该库的建表、迁移和读写权限。公网域名须绑定函数的 `$DEFAULT` 别名并配置 HTTPS，OAuth 应用须登记该域名的 `/oauth/callback`。

部署先更新代码，再发布版本并启动一个预置实例，待实例就绪后切换 `$DEFAULT` 流量。预置实例用于持续运行证书任务和自动续签调度，会产生持续的实例费用。旧版本的预置实例在切换后释放。部署包与 CLI 状态仅保存在 `.tmp/`，不上传为 GitHub artifact；部署结束会清理含 SDK 凭据的文件和临时包。
