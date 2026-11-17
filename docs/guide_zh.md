# CertCenter

证书管理、ACME 签发和部署服务。

## 本地模式

未填写 ConfigCenter 连接参数时，服务将设置保存到 `.tmp/certcenter-config.kv`，应用数据保存到 `.tmp/certcenter.db`，首次初始化设置时生成加密密钥。保留业务数据时，也应保留配置存储和其中的加密密钥。默认监听 `127.0.0.1:8081`，公网地址默认为 `http://127.0.0.1:8081`。

默认没有 OAuth 提供商，首次启动可打开登录页但不能登录。需要通过下述远程配置流程准备身份提供商和证书参数。

## 引导配置

仅以下参数从环境或可选的 `.env.local` 读取：

| 变量                          | 用途 / 默认值                                        |
| ----------------------------- | ---------------------------------------------------- |
| `CC_ENV_FILE`                 | 指定其他引导文件，默认 `.env.local`                  |
| `CC_CONFIGCENTER_URL`         | ConfigCenter 地址；与 API 密钥同时省略时使用本地模式 |
| `CC_CONFIGCENTER_API_KEY`     | 指定项目范围的 ConfigCenter 密钥                     |
| `CC_CONFIGCENTER_ENVIRONMENT` | 配置环境，默认 `production`                          |
| `CC_CONFIGCENTER_LOCAL_PATH`  | 本地设置文件，默认 `.tmp/certcenter-config.kv`       |

监听地址、数据库、OAuth 和加密密钥均来自配置存储，不通过任意 `CC_ADDR`、`CC_DB` 环境变量覆盖。

## 配置可用服务

1. 在 ConfigCenter 创建独立项目，签发可以读取配置、创建缺省项和写入配置的密钥，并填写引导连接参数。
2. 启动 CertCenter 一次，让它初始化缺失的 `certcenter.server` 和 `certcenter.providers`，已有值不会被覆盖。准备首次生产配置时先停止服务。
3. 在 ConfigCenter 编辑 `certcenter.server`：设置 `public_url`、`addr`、数据库参数、`oauth.issuer_url`、`oauth.client_id`、`oauth.client_secret`、`oauth.redirect_url`，以及目标管理员已验证邮箱对应的 `bootstrap.admin_email`；保留生成的 `encryption_key`。字段结构见[服务器配置](../internal/config/config.go)和 [OAuth 配置](../internal/auth/auth.go)。
4. 在身份提供商登记公网地址加 `/oauth/callback`。首次生产启动前，将 `database_dsn` 指向新的空应用数据库再重启：前面的初始化使用了默认管理员身份，修改引导参数不会重写已有用户；新的空生产数据库会按照已准备好的配置创建目标管理员。

接入 AuthCenter 时，按其签名配置设置 `certcenter.server` 的 `oauth.scopes`：

- 已配置 `AC_OIDC_SIGNING_KEYS` 时，使用 `["openid", "profile", "email"]`，通过公开的 JWKS 验证签名 ID Token。
- 未配置签名密钥时，使用 `["profile", "email"]`；共享客户端会省略不受支持的 `openid` 和 `nonce`，保留 PKCE，并在未签发 ID Token 时通过用户信息接口读取身份。

AuthCenter 不支持 `groups` scope。
5. 配置 ACME 账号、DNS 提供商、域名、证书策略及上游部署目标。提供商参数保存在 `certcenter.providers`，域名和任务由应用管理。

## 生产环境

使用 HTTPS 公网地址、持久配置存储和适合部署环境的数据库。加密证书材料、提供商凭据和加密密钥由各自的配置存储保管。GitHub 工作流提供腾讯 SCF 打包与部署入口；部署凭据保留在业务仓库的环境和 secrets 中。

## 共享基础设施

本项目通过 `common.lock.json` 固定 center-common 的提交，公共源码与 CI 步骤均来自同一个版本。首次检出执行 `make bootstrap`，再运行 `make check build`。私有依赖使用本机 Git 凭据；CI 配置只读 `COMMON_REPO_SSH_KEY` deploy key（或 `COMMON_REPO_TOKEN`）secret。

本地联动执行 `make common-local COMMON_DIR=../center-common && make bootstrap`。三个业务仓库通过 `.tmp/common` 直接引用同一份公共源码。Vue 控件在开发服务器热更新，Go 修改需重新构建。可复现部署执行 `CI=true make bootstrap check build`，忽略本地覆盖并拉取锁定提交。

升级时先提交并发布 common，再更新本项目锁文件与适配；回滚恢复原版本。完整说明见 [center-common README](https://github.com/OXeu/center-common#readme)。
