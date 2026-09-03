# 任务系统常用数据库操作

本文用于尚未建设管理员后台时，通过 MySQL 8+ 管理 `launcher_task_*` 表。

先应用结构迁移：

```powershell
Get-Content -Raw .\migrations\005_launcher_task_system.sql |
  mysql --default-character-set=utf8mb4 -u star_admin -p db_star
```

生产环境建议使用单独的任务配置账号。配置任务需要对 `launcher_task_campaign`、`launcher_task_group`、`launcher_task_definition`、`launcher_task_reward` 和 `launcher_task_legacy_binding` 拥有 `SELECT/INSERT/UPDATE` 权限，但不应允许它直接修改玩家进度、领取账本或货币库存。

## 字段速查

### Campaign 类型

| `kind` | 用途 |
| --- | --- |
| `onboarding` | 一次性新手旅程 |
| `event` | 有开始和结束时间的活动 |
| `season` | 赛季或通行证 |
| `evergreen` | 长期常驻任务集 |

### Group 分类和规则

| 字段 | 常用值 |
| --- | --- |
| `category` | `onboarding` / `daily` / `weekly` / `event` / `season` |
| `repeat_policy` | `once` / `daily` / `weekly` / `campaign` |
| `unlock_policy` | `sequential` 按顺序，`parallel` 无顺序 |
| `completion_rule` | `all` 全部完成，`any` 任一完成，`count` 完成指定数量 |

使用 `completion_rule='count'` 时必须填写 `required_task_count`；其他规则必须保持为 `NULL`。

### Task 进度和领取

| 字段 | 常用值 |
| --- | --- |
| `progress_source` | 新任务用 `native`；旧通行证桥接用 `legacy` |
| `aggregation` | `count`、`sum`、`distinct`、`max`、`boolean` |
| `claim_policy` | `manual` 登陆器领取，`automatic` 自动发放，`external` 游戏内领取 |

已发布任务不要直接修改目标和奖励，应创建新 revision。

## 创建一个完整的新手任务组

下面创建一个按顺序完成的 ZE 新手旅程。首次执行前修改代码、标题和奖励；代码一旦发布便不要复用。

```sql
START TRANSACTION;

INSERT INTO launcher_task_campaign
    (code, kind, title, description, timezone, status, sort_order)
VALUES
    ('onboarding-ze', 'onboarding', 'ZE 新手旅程',
     '帮助第一次进入 ZE 的玩家熟悉基础流程。', 'Asia/Shanghai', 'draft', 120);

SET @campaign_id = LAST_INSERT_ID();

INSERT INTO launcher_task_group
    (campaign_id, code, category, title, description, repeat_policy,
     completion_rule, required_task_count, unlock_policy, enabled, sort_order)
VALUES
    (@campaign_id, 'ze-basics', 'onboarding', 'ZE · 逃出生天',
     '按顺序完成第一次进服、通关和使用道具。', 'once',
     'all', NULL, 'sequential', 1, 10);

SET @group_id = LAST_INSERT_ID();

INSERT INTO launcher_task_definition
    (group_id, code, revision, series_code, title, description,
     progress_source, metric_key, aggregation, target_value, unit,
     claim_policy, criteria_json, enabled, sort_order, published_at)
VALUES
    (@group_id, 'enter-server', 1, 'ze-basics', '进入 ZE 服务器',
     '首次进入任意 ZE 服务器。', 'native', 'server.joined', 'boolean', 1, '次',
     'manual', JSON_OBJECT('mode', 'ZE'), 1, 10, NULL),
    (@group_id, 'finish-map', 1, 'ze-basics', '完成一次逃生',
     '在 ZE 模式中完成一次地图结算。', 'native', 'map.completed', 'count', 1, '次',
     'manual', JSON_OBJECT('mode', 'ZE'), 1, 20, NULL),
    (@group_id, 'use-item', 1, 'ze-basics', '使用模式道具',
     '在 ZE 模式中使用一次模式道具。', 'native', 'mode.item_used', 'count', 1, '次',
     'manual', JSON_OBJECT('mode', 'ZE'), 1, 30, NULL);

-- 每个任务自己的奖励。
INSERT INTO launcher_task_reward
    (task_id, group_id, reward_type, reward_ref, amount, sort_order)
SELECT id, NULL, 'starlight', '', 80, 10
FROM launcher_task_definition
WHERE group_id = @group_id AND code = 'enter-server' AND revision = 1;

INSERT INTO launcher_task_reward
    (task_id, group_id, reward_type, reward_ref, amount, sort_order)
SELECT id, NULL, 'season_exp', '', 120, 10
FROM launcher_task_definition
WHERE group_id = @group_id AND code = 'finish-map' AND revision = 1;

INSERT INTO launcher_task_reward
    (task_id, group_id, reward_type, reward_ref, amount, sort_order)
SELECT id, NULL, 'item', 'ze_rookie_card', 1, 10
FROM launcher_task_definition
WHERE group_id = @group_id AND code = 'use-item' AND revision = 1;

-- 完成整组后的额外奖励。
INSERT INTO launcher_task_reward
    (task_id, group_id, reward_type, reward_ref, amount, sort_order)
VALUES
    (NULL, @group_id, 'stardust', '', 25, 10);

COMMIT;
```

## 创建无顺序活动任务组

无顺序任务使用 `unlock_policy='parallel'`。下面要求四项中完成三项：

```sql
START TRANSACTION;

INSERT INTO launcher_task_campaign
    (code, kind, title, description, starts_at, ends_at, timezone, status, sort_order)
VALUES
    ('event-autumn-2026', 'event', '2026 秋日行动', '限时社区活动。',
     '2026-09-10 04:00:00', '2026-09-24 04:00:00',
     'Asia/Shanghai', 'draft', 50);

SET @campaign_id = LAST_INSERT_ID();

INSERT INTO launcher_task_group
    (campaign_id, code, category, title, description, repeat_policy,
     completion_rule, required_task_count, unlock_policy, enabled, sort_order)
VALUES
    (@campaign_id, 'autumn-free-play', 'event', '自由行动',
     '任选四个目标中的三个完成。', 'campaign',
     'count', 3, 'parallel', 1, 10);

SET @group_id = LAST_INSERT_ID();

INSERT INTO launcher_task_definition
    (group_id, code, revision, title, description, progress_source,
     metric_key, aggregation, target_value, unit, claim_policy,
     criteria_json, enabled, sort_order)
VALUES
    (@group_id, 'play-zm', 1, '完成 ZM 对局', '完整结算 3 局 ZM。',
     'native', 'match.completed', 'count', 3, '局', 'manual', JSON_OBJECT('mode', 'ZM'), 1, 10),
    (@group_id, 'play-ttt', 1, '完成 TTT 对局', '完整结算 3 局 TTT。',
     'native', 'match.completed', 'count', 3, '局', 'manual', JSON_OBJECT('mode', 'TTT'), 1, 20),
    (@group_id, 'unique-maps', 1, '探索地图', '游玩 4 张不同地图。',
     'native', 'map.played', 'distinct', 4, '张', 'manual', NULL, 1, 30),
    (@group_id, 'online-time', 1, '保持活跃', '累计有效在线 120 分钟。',
     'native', 'online.minutes', 'sum', 120, '分钟', 'manual', NULL, 1, 40);

COMMIT;
```

`distinct` 任务上报事件时必须携带稳定的 `distinctKey`，例如规范化后的地图名；后端只有首次插入 `launcher_player_task_distinct_value` 时才增加进度。

## 给已有任务新增奖励

先确认只匹配一条具体 revision：

```sql
SELECT d.id, c.code AS campaign_code, g.code AS group_code,
       d.code AS task_code, d.revision, d.title
FROM launcher_task_definition d
JOIN launcher_task_group g ON g.id = d.group_id
JOIN launcher_task_campaign c ON c.id = g.campaign_id
WHERE c.code = 'onboarding-ze'
  AND g.code = 'ze-basics'
  AND d.code = 'finish-map'
  AND d.revision = 1;
```

确认后添加奖励：

```sql
INSERT INTO launcher_task_reward
    (task_id, group_id, reward_type, reward_ref, amount, sort_order)
SELECT d.id, NULL, 'starlight', '', 100, 20
FROM launcher_task_definition d
JOIN launcher_task_group g ON g.id = d.group_id
JOIN launcher_task_campaign c ON c.id = g.campaign_id
WHERE c.code = 'onboarding-ze'
  AND g.code = 'ze-basics'
  AND d.code = 'finish-map'
  AND d.revision = 1;
```

已有人领取的任务新增奖励不会补发，且领取账本中的 `reward_snapshot` 不会改变。需要补发时应使用独立补偿流程，不能修改旧领取记录。

## 检查并发布 Campaign

发布前检查任务、奖励和时间窗：

```sql
SELECT c.code AS campaign_code, c.status, c.starts_at, c.ends_at,
       g.code AS group_code, g.category, g.repeat_policy, g.unlock_policy,
       d.code AS task_code, d.revision, d.metric_key, d.target_value,
       d.claim_policy, COUNT(r.id) AS reward_count
FROM launcher_task_campaign c
JOIN launcher_task_group g ON g.campaign_id = c.id
JOIN launcher_task_definition d ON d.group_id = g.id
LEFT JOIN launcher_task_reward r ON r.task_id = d.id
WHERE c.code = 'onboarding-ze'
GROUP BY c.id, g.id, d.id
ORDER BY g.sort_order, d.sort_order;
```

发布任务定义和 Campaign：

```sql
START TRANSACTION;

UPDATE launcher_task_definition d
JOIN launcher_task_group g ON g.id = d.group_id
JOIN launcher_task_campaign c ON c.id = g.campaign_id
SET d.published_at = COALESCE(d.published_at, CURRENT_TIMESTAMP(6))
WHERE c.code = 'onboarding-ze'
  AND d.enabled = 1
  AND d.retired_at IS NULL;

UPDATE launcher_task_campaign
SET status = 'published'
WHERE code = 'onboarding-ze' AND status = 'draft';

COMMIT;
```

## 修改已发布任务

不要修改旧行的 `target_value`、`criteria_json` 或奖励。创建新 revision，并在同一事务中退役旧版本：

```sql
START TRANSACTION;

SELECT d.id INTO @old_task_id
FROM launcher_task_definition d
JOIN launcher_task_group g ON g.id = d.group_id
JOIN launcher_task_campaign c ON c.id = g.campaign_id
WHERE c.code = 'onboarding-ze'
  AND g.code = 'ze-basics'
  AND d.code = 'finish-map'
  AND d.retired_at IS NULL
ORDER BY d.revision DESC
LIMIT 1
FOR UPDATE;

SET @group_id = (SELECT group_id FROM launcher_task_definition WHERE id = @old_task_id);
SET @next_revision = (SELECT revision + 1 FROM launcher_task_definition WHERE id = @old_task_id);

UPDATE launcher_task_definition
SET retired_at = CURRENT_TIMESTAMP(6), enabled = 0
WHERE id = @old_task_id;

INSERT INTO launcher_task_definition
    (group_id, code, revision, series_code, title, description,
     progress_source, metric_key, aggregation, target_value, unit,
     claim_policy, criteria_json, enabled, sort_order, published_at)
SELECT group_id, code, @next_revision, series_code, title,
       '在 ZE 模式中完成两次地图结算。', progress_source,
       metric_key, aggregation, 2, unit, claim_policy, criteria_json,
       1, sort_order, CURRENT_TIMESTAMP(6)
FROM launcher_task_definition
WHERE id = @old_task_id;

SET @new_task_id = LAST_INSERT_ID();

INSERT INTO launcher_task_reward
    (task_id, group_id, reward_type, reward_ref, amount, sort_order, metadata_json)
SELECT @new_task_id, NULL, reward_type, reward_ref, amount, sort_order, metadata_json
FROM launcher_task_reward
WHERE task_id = @old_task_id;

COMMIT;
```

旧 revision 的玩家进度继续引用旧 ID，不会被新目标重新解释。新 revision 只影响之后生成的新周期。一次性任务是否迁移旧进度必须单独评估，不能自动复制。

## 暂停、结束与归档

临时隐藏某个任务组：

```sql
UPDATE launcher_task_group g
JOIN launcher_task_campaign c ON c.id = g.campaign_id
SET g.enabled = 0
WHERE c.code = 'event-autumn-2026'
  AND g.code = 'autumn-free-play';
```

提前结束活动：

```sql
UPDATE launcher_task_campaign
SET ends_at = CURRENT_TIMESTAMP(6)
WHERE code = 'event-autumn-2026'
  AND status = 'published';
```

归档已结束活动：

```sql
UPDATE launcher_task_campaign
SET status = 'archived'
WHERE code = 'event-autumn-2026'
  AND ends_at <= CURRENT_TIMESTAMP(6);
```

只有从未发布、没有玩家进度和领取记录的草稿才允许物理删除。其他情况只归档或禁用。

## 查看玩家任务状态

```sql
SET @steam_id64 = 76561198000000000;

SELECT c.code AS campaign_code, g.code AS group_code, d.code AS task_code,
       p.period_key, p.progress_value, d.target_value, p.state,
       p.completed_at, p.claimed_at, p.updated_at
FROM launcher_player_task_progress p
JOIN launcher_task_definition d ON d.id = p.task_id
JOIN launcher_task_group g ON g.id = d.group_id
JOIN launcher_task_campaign c ON c.id = g.campaign_id
WHERE p.steam_id64 = @steam_id64
ORDER BY p.updated_at DESC;

SELECT id, idempotency_key, task_id, group_id, period_key,
       status, reward_snapshot, error_code, created_at, granted_at
FROM launcher_task_claim
WHERE steam_id64 = @steam_id64
ORDER BY id DESC;
```

不要通过直接把 `state` 改为 `claimed` 来补发奖励；这会绕过领取账本和下游货币/库存操作。

## 绑定旧通行证任务

仅在旧游戏插件仍是进度与奖励权威时使用：

```sql
INSERT INTO launcher_task_legacy_binding
    (task_id, provider, season_id, legacy_scope, legacy_quest_key,
     progress_metric, claimed_status_min)
VALUES
    (@task_id, 'season_pass', 0, 'daily', '8', 'daily_games', 2);
```

绑定后的任务必须满足：

```sql
UPDATE launcher_task_definition
SET progress_source = 'legacy', claim_policy = 'external'
WHERE id = @task_id;
```

同一旧 quest 只能绑定一次。不要同时向 `launcher_player_task_progress` 写入相同任务，否则会形成两套权威数据。

## 常用排查

检查活动时间窗：

```sql
SELECT code, status, starts_at, ends_at,
       starts_at IS NULL OR starts_at <= CURRENT_TIMESTAMP(6) AS has_started,
       ends_at IS NULL OR ends_at > CURRENT_TIMESTAMP(6) AS is_active
FROM launcher_task_campaign
ORDER BY sort_order, id;
```

检查同一任务是否意外存在多个有效 revision：

```sql
SELECT group_id, code, COUNT(*) AS active_revisions
FROM launcher_task_definition
WHERE enabled = 1
  AND (published_at IS NULL OR published_at <= CURRENT_TIMESTAMP(6))
  AND (retired_at IS NULL OR retired_at > CURRENT_TIMESTAMP(6))
GROUP BY group_id, code
HAVING COUNT(*) > 1;
```

检查卡住的领取任务：

```sql
SELECT id, steam_id64, task_id, group_id, period_key,
       status, error_code, updated_at
FROM launcher_task_claim
WHERE status IN ('pending', 'failed')
ORDER BY updated_at ASC
LIMIT 100;
```
