# Launcher 任务系统数据库方案

## 目标

新版任务页同时支持：

- 新手引导任务组；
- 每日、每周和长期常驻任务；
- 有起止时间的活动任务组；
- 赛季长期任务与通行证概览；
- 现有游戏内通行证写死任务，不要求立即改动游戏插件或旧表。

迁移文件为 `migrations/005_launcher_task_system.sql`，目标数据库为 MySQL 8+。

## 核心边界

任务系统分成三层：

1. `campaign` 管生命周期，例如“新手旅程”“雾都活动”“第 8 赛季”。
2. `group` 管 UI 分类、重复周期和阶段组织，例如“每日游玩”“通关地图 1/3/5 次”。
3. `definition` 是可完成、可领取的最小任务节点。

通行证不是任务定义的唯一容器。它只是一种 `season` campaign，同时可以包含每日、每周和赛季分类的任务组。这样新手引导不会被迫套进通行证模型。

## 表职责

| 表 | 职责 |
| --- | --- |
| `launcher_task_campaign` | 活动/赛季/新手/常驻任务集的发布时间窗与展示元数据 |
| `launcher_task_group` | 页面分组、分类、刷新周期、并行或顺序解锁规则 |
| `launcher_task_definition` | 指标、目标值、单位、筛选条件、领取策略及不可变版本 |
| `launcher_task_reward` | 任务奖励或整组完成奖励 |
| `launcher_player_task_progress` | 新任务的玩家周期进度 |
| `launcher_player_task_distinct_value` | `distinct` 指标的去重值，例如已体验模式或已通关地图 |
| `launcher_player_task_group_state` | 整组完成与整组奖励领取状态 |
| `launcher_task_claim` | 幂等领取账本与奖励快照 |
| `launcher_task_event_inbox` | 游戏服上报事件的去重收件箱 |
| `launcher_task_legacy_binding` | 把旧通行证 quest ID 和累计字段映射成统一任务 |

## 为什么分类放在 group

`campaign.kind` 描述生命周期；`group.category` 描述登陆器放在哪个 Tab：

- 新手 campaign → `onboarding` group；
- 限时活动 campaign → `event` group；
- 通行证 campaign → 可以同时拥有 `daily`、`weekly`、`season` group；
- 常驻 campaign → 可以拥有每日或每周 group。

因此页面信息结构和任务有效期不会被一个枚举强行绑定。

## 周期键

玩家进度主键包含 `period_key`，避免刷新时覆盖历史记录：

- 一次性新手任务：`once`；
- 每日任务：`2026-09-02`；
- 每周任务：`2026-W36`；
- 活动或赛季任务：`campaign:<campaign-code>`。

周期必须由后端按照 campaign 的 `timezone` 生成，不能接受客户端传入。默认时区是 `Asia/Shanghai`。

## 旧通行证兼容方式

旧通行证当前使用：

- `season_pass_players`：等级、经验、已领通行证礼包；
- `season_pass_daily_quests`：当日累计值和 `quest_status`；
- `season_pass_weekly_quests`：当周累计值和 `quest_status`；
- 登陆器内固定标题、目标值和 quest ID。

迁移会为这 12 个任务创建 `progress_source='legacy'` 定义和绑定，但不会复制玩家进度：

| 周期 | 旧 quest ID | 进度来源 | 目标 |
| --- | ---: | --- | ---: |
| 每日 | 1 | `has_logged_in` | 1 |
| 每日 | 2 / 3 / 4 | `games_completed` | 1 / 3 / 5 |
| 每日 | 5 / 6 / 7 | `online_minutes` | 10 / 30 / 60 |
| 每周 | 101 | `has_logged_in` | 1 |
| 每周 | 102 / 103 / 104 | `games_completed` | 1 / 5 / 10 |
| 每周 | 105 | `completed_modes` 的元素数 | 3 |

统一状态投影规则：

1. `quest_status[questId] >= claimed_status_min` → `claimed`；
2. 否则旧累计值达到 `target_value` → `completed`；
3. 否则 → `in_progress`。

旧任务的 `claim_policy` 为 `external`。登陆器可以展示状态，但迁移阶段仍提示玩家在游戏内领取；后端不能同时写新表和旧表，否则会出现重复奖励和状态分叉。

旧通行证奖励目前没有可靠的数据库定义，因此桥接任务不伪造奖励明细。等游戏插件提供确定的奖励表后，再写入 `launcher_task_reward`。

## 后端合并流程

后端已提供只读的 `GET /api/v1/me/tasks`，避免继续膨胀登录响应：

1. 查询当前已发布且位于有效时间窗内的 campaign/group/definition；
2. 对 `native` 任务按服务端生成的 `period_key` 读取新进度表；
3. 如果存在 `legacy` 绑定，只执行一次现有 `SeasonPass()` 查询；
4. 按绑定表把旧字段投影成统一任务状态；
5. 按 campaign/group/sort 顺序返回任务树和通行证摘要。

旧 `/api/v1/auth/login` 中的 `seasonPass` 字段暂时保持不变，避免个人页和旧版本登陆器立即失效。新版任务页改读 `/api/v1/me/tasks` 后，两套响应可以并存一个发布周期。

统一 ID 建议：

- 原生任务：`task:<definition-id>:<period-key>`；
- 旧任务：`legacy:season_pass:<season-id>:<scope>:<quest-id>`。

客户端只把 ID 当作不透明字符串，不能解析它决定业务逻辑。

## 原生任务进度

游戏服通过 StarCore 的统一任务事件接口，把游戏事实批量提交到受保护的
`POST /internal/v1/task-events/batch`。请求使用与游戏服控制面一致的
`X-Star-Api-Key`，单批最多 100 条。每条事件都必须带 UUID，例如：

```json
{
  "serverId": "zm-01",
  "events": [{
    "eventId": "4d62e911-b553-4181-8a13-d81b38fa35e7",
    "source": "ZombieZeta",
    "steamId": "76561198000000000",
    "metric": "match.completed",
    "value": 1,
    "distinctKey": "zm_example",
    "dimensions": {
      "mode": "ZM",
      "map": "zm_example",
      "won": true
    },
    "occurredAt": "2026-09-02T12:00:00Z"
  }]
}
```

处理器逐条在事务中以 `event_id` 插入 inbox 去重，再匹配
`metric_key + criteria_json` 并更新符合条件的原生任务。重复批次返回成功但计入
`duplicates`，因此 StarCore 可以安全重试整个批次。`distinct` 任务必须使用稳定的
`distinctKey`，例如地图名或模式名，并先写入
`launcher_player_task_distinct_value`；只有首次插入成功时才增加进度。不能只依赖
event ID，因为同一地图可能产生多个不同事件。

玩法插件不能传入 task ID 或直接设置完成状态。它们只上报 metric；任务匹配、周期、
目标值和完成状态均由后端控制。游戏服接口只接受正向值，人工回退或修正应使用单独的
管理员接口，避免普通玩法插件意外扣减任务进度。

新手任务中的“打开库存”等纯登陆器行为也必须上报后端；不能只在本地标记，否则更换设备后会重新出现。

## 领取事务

`POST /api/v1/me/tasks/{id}/claim` 仅允许 `claim_policy='manual'` 的原生任务：

1. 再次验证登录密码和会话；
2. `SELECT ... FOR UPDATE` 锁定玩家进度；
3. 校验状态为 `completed`；
4. 生成服务端 idempotency key，并写入 `launcher_task_claim`；
5. 将奖励定义冻结到 `reward_snapshot`；
6. 发放货币/物品；
7. 同事务更新 claim 为 `granted`、进度为 `claimed`。

若不同奖励位于不同数据库，不能假装存在跨库事务。应先写 `pending` 领取记录，再由可重试 worker 发放；每个奖励下游也要使用同一领取 ID 去重。只有全部发放完成才返回成功。

## 发布与修改规则

- 已经 `published` 且有人产生进度的 definition 不原地修改目标或奖励；创建新 `revision`。
- 玩家进度始终引用具体 definition ID，所以旧周期不会被新版本重新解释。
- 活动结束只停止产生新进度，历史领取记录不能删除。
- 任务隐藏使用 `enabled=0` 或 campaign `archived`，不要物理删除有玩家记录的定义。
- 奖励在领取时复制到 `reward_snapshot`，管理员后续改配置不会改变重试结果。

## 上线顺序

1. 应用 `005_launcher_task_system.sql`，仅建表和登记旧任务映射；现有逻辑不受影响。
2. 后端实现只读的统一任务查询与 legacy adapter。（已完成）
3. 登陆器任务页从 Demo 数据切换到查询接口；旧 Profile 通行证保留。
4. 接入游戏服事件上报，先启用新手或小活动任务验证原生进度。
5. 实现幂等领取和奖励 worker。
6. 确认游戏内旧通行证迁移计划后，再决定是否把 legacy 任务改为原生任务；不要双写过渡。
