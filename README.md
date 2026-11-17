# CertCenter

证书管理、ACME 签发和部署服务。

## 共享基础设施

本项目通过 `common.lock.json` 固定 center-common 的提交，公共源码与 CI 步骤均来自同一个版本。首次检出执行 `make bootstrap`，再运行 `make check build`。私有依赖使用本机 Git 凭据；CI 配置只读 `COMMON_REPO_SSH_KEY` deploy key（或 `COMMON_REPO_TOKEN`）secret。

本地联动执行 `make common-local COMMON_DIR=../center-common && make bootstrap`。三个业务仓库通过 `.tmp/common` 直接引用同一份公共源码。Vue 控件在开发服务器热更新，Go 修改需重新构建。可复现部署执行 `CI=true make bootstrap check build`，忽略本地覆盖并拉取锁定提交。

升级时先提交并发布 common，再更新本项目锁文件与适配；回滚恢复原版本。完整说明见 [center-common README](https://github.com/OXeu/center-common#readme)。
