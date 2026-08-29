# STAR Launcher Backend

给 STAR Launcher 使用的轻量 Go HTTP API。公告与商城仍使用展示数据；账号登录会校验真实数据库，登录成功后从 `sls_player_inventory` 读取该 Steam64 的真实库存。

## 要求

- Go 1.22 或更高版本

## 启动

```powershell
go test ./...
go run ./cmd/server
```

默认监听 `http://localhost:8080`。可通过环境变量调整：

```powershell
$env:STAR_BACKEND_ADDR = ":8080"
$env:STAR_CORS_ORIGINS = "http://localhost:1420,http://tauri.localhost,https://tauri.localhost,tauri://localhost"
$env:STAR_DB_DSN = "数据库用户:数据库密码@tcp(mysql.example.com:3306)/db_star?charset=utf8mb4&parseTime=true&loc=Local&timeout=5s&readTimeout=8s&writeTimeout=8s"
go run ./cmd/server
```

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
| GET | `/api/v1/announcements` | 公告列表 |
| GET | `/api/v1/store/items` | 商城商品，可用 `currency=starlight` 或 `currency=stardust` 筛选 |
| GET | `/api/v1/me` | 演示用户资料、钱包及兑换比例 |
| POST | `/api/v1/auth/login` | 使用 Steam64 与游戏内密码登录，并返回真实库存 |
| GET | `/api/v1/me/inventory` | 使用 Bearer 会话读取真实库存 |

示例：

```powershell
Invoke-RestMethod http://localhost:8080/api/v1/bootstrap
Invoke-RestMethod 'http://localhost:8080/api/v1/store/items?currency=starlight'
```

登录示例：

```powershell
$login = Invoke-RestMethod -Method Post -ContentType 'application/json' -Body '{"steamId":"7656119xxxxxxxxxx","password":"游戏内密码"}' http://localhost:8080/api/v1/auth/login
Invoke-RestMethod -Headers @{ Authorization = "Bearer $($login.data.token)" } http://localhost:8080/api/v1/me/inventory
```

## 当前边界

- 真实库存联表为 `sls_player_inventory`、`sls_product` 与 `sls_product_rarity`。
- 登录校验使用 `scs_user` 与 `scs_user_steamid`，当前按数据库现有明文密码字段比较；若线上使用了额外哈希规则，需要同步对应算法。
- 登录会话保存在后端内存中，有效期 24 小时；服务重启后需要重新登录。
- `/api/v1/me`、公告、钱包和商城商品目前仍是展示数据。
- 充值、兑换和购买暂时没有写接口，避免演示阶段产生伪交易语义。
- 暂不修改真实库存数量，也不写入装备配置；数据库结构中没有明确的装备配置表与物品使用副作用定义。
- 商品的 `icon` 与 `tone` 返回前端可识别的资源键和 Tailwind 色调键。
