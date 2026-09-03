# STAR Launcher Backend

给 STAR Launcher 使用的轻量 Go HTTP API。启动器主要通过 `/api/v1/bootstrap` 与登录后会话接口取数；账号登录会校验真实数据库，登录成功后从 `sls_player_inventory` 读取该 Steam64 的真实库存。

## 要求

- Go 1.22 或更高版本

## 启动

```powershell
go test ./...
go run ./cmd/server
```

默认监听 `http://localhost:8080`。程序会自动读取当前工作目录、exe 所在目录或 exe 上级目录中的第一个 `.env`；进程环境变量优先于 `.env`。也可以直接设置环境变量：

```powershell
$env:STAR_BACKEND_ADDR = ":8080"
$env:STAR_CORS_ORIGINS = "http://localhost:1420,http://tauri.localhost,https://tauri.localhost,tauri://localhost"
$env:STAR_SKIP_PASSWORD_AUTH = "true" # 只读开发期临时跳过游戏内密码校验
$env:STAR_GAME_API_KEY = "与游戏服 Star-Core 配置一致的长随机密钥"
$env:STAR_CLIENT_PREFS_API_URL = "Star-Core public.json 中的 api.base_url"
$env:STAR_CLIENT_PREFS_API_KEY = "Star-Core public.json 中的 api.api_key"
$env:STAR_CLIENT_PREFS_API_KEY_HEADER = "X-Star-Api-Key"
$env:HTTPS_PROXY = "http://127.0.0.1:7890" # 可选：Steam 组接口无法直连时配置；不会自动读取 Windows 系统代理
$env:STAR_DB_USER = "数据库用户"
$env:STAR_DB_PASSWORD = "数据库密码"
$env:STAR_DB_HOST = "mysql.example.com"
$env:STAR_DB_PORT = "3306"
$env:STAR_DB_NAME = "db_star"
$env:STAR_DB_CHALLENGE_DSN = "challenge_reader:密码@tcp(主机:3306)/db_challenge?parseTime=true"
go run ./cmd/server
```

### 导入星尘商品目录

`DB_CHALLENGE` 运行时使用 `STAR_DB_CHALLENGE_DSN` 只读连接。先由有建表权限的账号应用迁移：

```powershell
Get-Content -Raw .\migrations\001_starduststore_catalog.sql | mysql --default-character-set=utf8mb4 -u challenge_admin -p db_challenge
```

再单独配置具有目录表写权限的导入账号；运行时后端不会读取这个变量：

```powershell
$env:STAR_DB_CHALLENGE_IMPORT_DSN = "challenge_writer:密码@tcp(主机:3306)/db_challenge?parseTime=true"
go run -buildvcs=false ./cmd/import_stardust_catalog -file E:\Downloads\StarDustStore.json -dry-run
go run -buildvcs=false ./cmd/import_stardust_catalog -file E:\Downloads\StarDustStore.json -sql-out .\migrations\002_import_starduststore_catalog.sql
go run -buildvcs=false ./cmd/import_stardust_catalog -file E:\Downloads\StarDustStore.json
```

每次导入会按 `(type, uniqueid)` 更新目录，并停用新 JSON 中已不存在的旧目录项。星尘商城只展示目录中可购买且未隐藏的商品；玩家星尘库存与目录表进行内连接，目录中不存在或已停用的物品不会展示。

推荐使用上述分离字段，密码中的 `@`、`:`、`/` 等字符都可以原样填写。也支持完整的 `STAR_DB_DSN`，格式必须是 `用户名:完整密码@tcp(主机:端口)/数据库?...`；密码中的 `@` 无需改写为 `%40`。

## 接口

所有业务接口使用与 StarCS 服务器接口一致的响应外壳：

```json
{
  "code": 2000,
  "msg": "success",
  "data": {}
}
```

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/healthz` | 健康检查 |
| GET | `/api/v1/bootstrap` | 一次获取登录器展示所需的全部基础数据 |
| GET | `/api/v1/store/items` | 商城商品，可用 `currency=starlight` 或 `currency=stardust` 筛选 |
| GET | `/api/v1/workshop-packs?mode=ZM` | 获取公共基础资源包与指定模式资源包 |
| POST | `/api/v1/auth/login` | 使用 Steam64 与游戏内密码登录，并返回真实库存 |
| POST | `/api/v1/auth/verify` | 敏感操作前使用 Bearer 会话与当前密码复验 |
| GET | `/api/v1/me/tasks` | 读取统一任务树与旧通行证投影；需要 Bearer 会话和 `X-StarCS-Reauth` |
| GET | `/api/v1/me/inventory` | 使用 Bearer 会话读取真实库存，并通过 `X-StarCS-Reauth` 携带当前密码 |
| GET | `/api/v1/me/equipment` | 读取 StarLightStore 在所有支持模式中的真实装备配置 |
| POST | `/api/v1/me/equipment/equip` | 装备当前账号持有的角色/武器外观 |
| POST | `/api/v1/me/equipment/unequip` | 卸下当前账号持有的角色/武器外观 |
| POST | `/internal/v1/game-password` | 游戏服设置/修改 `star_user` 游戏密码；需要 `X-Star-Api-Key` |
| GET | `/internal/v1/ws/game` | 游戏服 WSS 控制面（Query: `serverId` 必填，`mode`/`hostname` 可选）；需要 `X-Star-Api-Key` |
| GET | `/internal/v1/servers` | 列出当前已连接的游戏服控制会话 |
| POST | `/internal/v1/servers/{serverId}/commands` | 向已连接游戏服下发白名单指令并等待回执；允许 `server.ping`、`server.info`、`player.reload_inventory`、`player.reload_prefs`、`player.reload_stardust` |

示例：

```powershell
Invoke-RestMethod http://localhost:8080/api/v1/bootstrap
Invoke-RestMethod 'http://localhost:8080/api/v1/store/items?currency=starlight'
Invoke-RestMethod 'http://localhost:8080/api/v1/workshop-packs?mode=ZM'
```

登录示例：

```powershell
$login = Invoke-RestMethod -Method Post -ContentType 'application/json' -Body '{"steamId":"7656119xxxxxxxxxx","password":"游戏内密码"}' http://localhost:8080/api/v1/auth/login
Invoke-RestMethod -Headers @{ Authorization = "Bearer $($login.data.token)"; 'X-StarCS-Reauth' = '游戏内密码' } http://localhost:8080/api/v1/me/inventory
Invoke-RestMethod -Headers @{ Authorization = "Bearer $($login.data.token)"; 'X-StarCS-Reauth' = '游戏内密码' } http://localhost:8080/api/v1/me/tasks
```

游戏服控制面示例（需游戏服已通过 WSS 连上）：

```powershell
$headers = @{ 'X-Star-Api-Key' = $env:STAR_GAME_API_KEY }
Invoke-RestMethod -Headers $headers http://localhost:8080/internal/v1/servers
Invoke-RestMethod -Method Post -Headers $headers -ContentType 'application/json' `
  -Body '{"name":"server.ping","payload":{},"timeoutMs":5000}' `
  http://localhost:8080/internal/v1/servers/zm-01/commands
Invoke-RestMethod -Method Post -Headers $headers -ContentType 'application/json' `
  -Body '{"name":"player.reload_inventory","payload":{"steamId":"7656119xxxxxxxxxx"},"timeoutMs":8000}' `
  http://localhost:8080/internal/v1/servers/zm-01/commands
```

## 当前边界

- 真实库存联表为 `sls_player_inventory`、`sls_product` 与 `sls_product_rarity`。
- 星尘余额、持有物品来自 `DB_CHALLENGE`；商品名称、分类和当前价格以 `starduststore_catalog` 为准。
- 玩家登录只使用 `star_user.game_password_hash`，不再读取后台管理员表 `scs_user`。密码以 Argon2id PHC 字符串保存，参数为 19 MiB 内存、2 次迭代、并行度 1。
- 游戏服通过 Steam 授权态确认玩家身份后，可携带 `identityValidated: true` 直接设置或重置密码，无需旧密码。Go 后端统一负责生成摘要，C# 插件不会自行实现哈希算法。
- 游戏服改密接口对参数或旧密码等业务失败保持 HTTP 200，并通过 `code/msg/data` 表达结果，以兼容 Star-Core 的标准 HTTP 客户端；API Key 失败和服务故障仍返回对应 HTTP 错误。
- 登陆器登录后，每次真实库存读取或装备等敏感操作都会再次提交当前密码校验。校验失败时该 Bearer 会话会立即失效，登陆器应同步退出登录。
- 统一任务接口只读取 `launcher_task_*` 表；旧通行证任务继续以 `season_pass_*` 为权威并通过 `launcher_task_legacy_binding` 投影。当前版本未开放任务事件写入和奖励领取接口。
- 装备配置不直接写库存数据库；后端通过 Star-Core 已有的 ClientPrefs API 更新 `star_light_store` 插件的 `p_s` 与 `w_s`，并保留同插件的其他偏好键。
- `STAR_SKIP_PASSWORD_AUTH=true` 时仅校验 Steam64 格式并签发 24 小时只读会话；默认关闭，不能直接用于公开生产环境。
- 登录会话保存在后端内存中，有效期 24 小时；服务重启后需要重新登录。
- 未配置数据库时，`/api/v1/bootstrap` 与 `/api/v1/store/items` 会回退到本地展示数据；已配置时公告、地图与商城走真实库，游客账号展示仍可能是占位资料。
- 游戏服可通过 WSS 出站连接 `/internal/v1/ws/game`，由后端经 `POST /internal/v1/servers/{serverId}/commands` 下发指令；同一 `serverId` 新连接会顶替旧连接。`player.reload_prefs` 由 StarCore 直接重载 ClientPrefs；`player.reload_inventory` / `player.reload_stardust` 仅在玩家在线时由 Core 派发控制台指令（`sls_reload_player_inventory` / `css_stardust_reload_player`）并立即回执，不等待商店插件结果。
- 商品的 `icon` 与 `tone` 返回前端可识别的资源键和 Tailwind 色调键。
