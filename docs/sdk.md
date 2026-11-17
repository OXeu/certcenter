# Go SDK

本仓库的 `sdk/configcenter` 是配置读写客户端，模块路径为 `github.com/OXeu/configcenter/sdk/configcenter`。它连接 **ConfigCenter** 或本地 bbolt KV，用于维护 CertCenter 的配置。它没有签发证书、查询任务或部署证书的方法；这些操作见 [CertCenter HTTP API](/docs/api)。服务配置字段和首次启动步骤见[配置指南](/docs/config)。

## 引入当前仓库的 SDK

SDK 是独立 Go 模块，要求 Go 1.26。若要使用本仓库包含的本地存储和批量接口，在调用方模块中指向当前 checkout（把路径换成实际绝对路径）：

```sh
go mod edit -require=github.com/OXeu/configcenter/sdk/configcenter@v0.0.0
go mod edit -replace=github.com/OXeu/configcenter/sdk/configcenter=/path/to/certcenter/sdk/configcenter
go mod tidy
```

`replace` 用于确保示例使用此仓库版本；不能假设其他仓库的已发布版本包含相同接口。

## 创建客户端并读取

下面是完整可编译的包。URL 和 API key 都为空时使用本地文件；否则必须同时提供，URL 应指向 ConfigCenter，而不是 CertCenter 的站点地址。远端读取使用 `Authorization: ApiKey cc_…`，需要单项目 key 的 `config:read` 权限。

```go
package integration

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/OXeu/configcenter/sdk/configcenter"
)

type ACME struct {
    DirectoryURL string `json:"directory_url"`
    Email        string `json:"email"`
    AcceptTerms  bool   `json:"accept_terms"`
}

func ReadACME(ctx context.Context, cfg configcenter.Config) (ACME, error) {
    var result ACME
    client, err := configcenter.New(cfg)
    if err != nil {
        return result, fmt.Errorf("create settings client: %w", err)
    }
    if err := client.Load(ctx); err != nil {
        return result, fmt.Errorf("load settings: %w", err)
    }
    raw, ok := client.Get("certcenter.providers")
    if !ok {
        return result, fmt.Errorf("certcenter.providers: %w", configcenter.ErrKeyNotFound)
    }
    var providers struct {
        ACME ACME `json:"acme"`
    }
    if err := json.Unmarshal([]byte(raw), &providers); err != nil {
        return result, fmt.Errorf("decode provider settings: %w", err)
    }
    return providers.ACME, nil
}
```

本地调用方传入 `configcenter.Config{LocalPath: "/path/to/certcenter/.tmp/certcenter-config.kv", Environment: "production"}`。文件路径和环境必须与 CertCenter 一致。默认路径是相对当前工作目录的 `.tmp/certcenter-config.kv`，默认环境为 `production`。

远端传入 `configcenter.Config{BaseURL: configCenterURL, APIKey: apiKey, Environment: "production"}`，其中地址和密钥由调用方提供。远端错误不会自动回退本地。`HTTPClient` 可自定义 HTTP 客户端；未提供时 `Timeout` 默认 30 秒。

## 方法与快照行为

| 方法                                                  | 行为                                                                  |
| ----------------------------------------------------- | --------------------------------------------------------------------- |
| `New(Config) (*Client, error)`                        | 校验连接配置；本地模式打开/初始化存储。远端模式不在此时请求服务       |
| `Load(ctx) error`                                     | 读取当前环境全部配置并替换内存快照                                    |
| `LoadPrefix(ctx, prefix) error`                       | 仅读取指定前缀并替换快照；不是与旧快照合并                            |
| `Get(key) (string, bool)`                             | 读取快照，不发起网络请求；缺失时返回 `false`                          |
| `GetDefault(key, fallback) string`                    | 缺失时返回默认字符串                                                  |
| `Int(key, fallback) int` / `Bool(key, fallback) bool` | 缺失或解析失败时返回默认值                                            |
| `All() []Item`                                        | 返回按键排序的快照副本；`Item` 含 key、value、description、updated_at |
| `ReadMany(ctx, keys) (map[string]string, error)`      | 直接读取指定键；缺失键被省略，不替换快照                              |
| `Put(ctx, key, value) error`                          | 创建或更新一个字符串值，更新匹配已加载前缀的快照                      |
| `PutMany(ctx, values) error`                          | 一个事务创建或更新整批值，更新匹配前缀的快照                          |
| `EnsureDefaults(ctx, defaults) error`                 | 只补缺失键，再重新加载完整快照；不修复既有空值或无效值                |
| `Watch(ctx, WatchOptions) error`                      | 先同步 `Load`，成功后启动后台轮询；取消 context 停止                  |
| `IsLocal() bool` / `Environment() string`             | 查询存储模式与环境                                                    |

批量方法接受 1–500 个键，远端批量写请求体最多 1 MiB。远端 `Put`、`PutMany` 需要 `projects:read`、`environments:read`、`config:write`；`EnsureDefaults` 还需要 `config:read`。SDK 通过 `/api/v1/agent/project` 解析 key 对应的项目，不接受任意项目参数。

`WatchOptions` 有 `Interval time.Duration` 和 `OnError func(error)`。非正间隔使用 30 秒；首次读取失败直接返回 error，后续失败通过 `OnError` 报告并继续重试。`Watch` 每次加载完整环境，即便此前调用过 `LoadPrefix`。轮询不会返回变更事件，调用方继续使用 `Get` / `All` 获取最新快照。

## 写入与错误处理

JSON 配置先通过 `json.Marshal` 编码，再将 `string(raw)` 传给 `Put` 或 `PutMany`。这些方法替换整个配置值，不合并 JSON 字段；修改 `certcenter.providers` 时必须保留 ACME 和其他账号，修改 `certcenter.server` 时必须保留已有加密密钥。SDK 不校验证书策略，业务校验由 CertCenter 完成。

`Get` 的缺失值由布尔值判断；`ErrKeyNotFound` 可供调用方包装缺失键错误。远端 HTTP 404 包装 `ErrNotFound`，可通过 `errors.Is` 判断。其他请求错误带状态信息，调用方需处理返回的 error；不要输出配置值或凭据。

本地初始化和批量写入使用 bbolt 事务。远端 `EnsureDefaults` 的读写不是跨请求事务，需由一个实例执行；SDK 没有比较交换（CAS），并发整项写入可能相互覆盖。`LocalDefaults` 仅在本地模式补齐缺失值，远端忽略它。
