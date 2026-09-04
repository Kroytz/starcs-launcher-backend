-- STAR Launcher unified task system (MySQL 8+)
--
-- Native launcher tasks store progress in the new tables below. Existing
-- season-pass tasks remain owned by season_pass_* and are exposed through the
-- legacy binding table seeded at the end of this migration.

CREATE TABLE IF NOT EXISTS launcher_task_campaign (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    code VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    kind ENUM('onboarding', 'event', 'season', 'evergreen') NOT NULL,
    title VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    starts_at DATETIME(6) NULL,
    ends_at DATETIME(6) NULL,
    timezone VARCHAR(64) CHARACTER SET ascii COLLATE ascii_general_ci NOT NULL DEFAULT 'Asia/Shanghai',
    status ENUM('draft', 'published', 'archived') NOT NULL DEFAULT 'draft',
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    metadata_json JSON NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_launcher_task_campaign_code (code),
    KEY idx_launcher_task_campaign_visible (status, starts_at, ends_at, sort_order),
    CONSTRAINT chk_launcher_task_campaign_window CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_task_group (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    campaign_id BIGINT UNSIGNED NOT NULL,
    code VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    category ENUM('onboarding', 'daily', 'weekly', 'event', 'season') NOT NULL,
    title VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    repeat_policy ENUM('once', 'daily', 'weekly', 'campaign') NOT NULL DEFAULT 'campaign',
    completion_rule ENUM('all', 'any', 'count') NOT NULL DEFAULT 'all',
    required_task_count INT UNSIGNED NULL,
    unlock_policy ENUM('parallel', 'sequential') NOT NULL DEFAULT 'parallel',
    enabled TINYINT(1) UNSIGNED NOT NULL DEFAULT 1,
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    metadata_json JSON NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_launcher_task_group_code (campaign_id, code),
    KEY idx_launcher_task_group_visible (campaign_id, category, enabled, sort_order),
    CONSTRAINT chk_launcher_task_group_required_count CHECK (
        (completion_rule = 'count' AND required_task_count IS NOT NULL AND required_task_count > 0)
        OR (completion_rule <> 'count' AND required_task_count IS NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_task_definition (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    group_id BIGINT UNSIGNED NOT NULL,
    code VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    revision INT UNSIGNED NOT NULL DEFAULT 1,
    series_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    title VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    progress_source ENUM('native', 'legacy') NOT NULL DEFAULT 'native',
    metric_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    aggregation ENUM('sum', 'count', 'distinct', 'max', 'boolean') NOT NULL DEFAULT 'count',
    target_value BIGINT UNSIGNED NOT NULL,
    unit VARCHAR(32) NOT NULL DEFAULT '次',
    claim_policy ENUM('manual', 'automatic', 'external') NOT NULL DEFAULT 'manual',
    criteria_json JSON NULL,
    enabled TINYINT(1) UNSIGNED NOT NULL DEFAULT 1,
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    published_at DATETIME(6) NULL,
    retired_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_launcher_task_definition_revision (group_id, code, revision),
    KEY idx_launcher_task_definition_visible (group_id, enabled, sort_order),
    KEY idx_launcher_task_definition_metric (progress_source, metric_key),
    CONSTRAINT chk_launcher_task_definition_target CHECK (target_value > 0),
    CONSTRAINT chk_launcher_task_definition_window CHECK (retired_at IS NULL OR published_at IS NULL OR retired_at > published_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_task_reward (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    task_id BIGINT UNSIGNED NULL,
    group_id BIGINT UNSIGNED NULL,
    reward_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'starlight/stardust/season_exp/item/...',
    reward_ref VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '物品 ID 等外部引用；纯货币可为空',
    amount BIGINT UNSIGNED NOT NULL DEFAULT 1,
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    metadata_json JSON NULL,
    PRIMARY KEY (id),
    KEY idx_launcher_task_reward_task (task_id, sort_order),
    KEY idx_launcher_task_reward_group (group_id, sort_order),
    CONSTRAINT chk_launcher_task_reward_owner CHECK ((task_id IS NULL) <> (group_id IS NULL)),
    CONSTRAINT chk_launcher_task_reward_amount CHECK (amount > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_task_legacy_binding (
    task_id BIGINT UNSIGNED NOT NULL,
    provider VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'season_pass',
    season_id INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0=任意当前赛季',
    legacy_scope ENUM('daily', 'weekly', 'season') NOT NULL,
    legacy_quest_key VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    progress_metric ENUM(
        'daily_logged_in', 'daily_games', 'daily_online_minutes',
        'weekly_logged_in', 'weekly_games', 'weekly_completed_modes',
        'season_level', 'season_experience'
    ) NOT NULL,
    claimed_status_min SMALLINT UNSIGNED NOT NULL DEFAULT 2,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (task_id),
    UNIQUE KEY uk_launcher_task_legacy_source (provider, season_id, legacy_scope, legacy_quest_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_player_task_progress (
    steam_id64 BIGINT UNSIGNED NOT NULL,
    task_id BIGINT UNSIGNED NOT NULL,
    period_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'once / YYYY-MM-DD / YYYY-Www / campaign:<code>',
    progress_value BIGINT UNSIGNED NOT NULL DEFAULT 0,
    state ENUM('in_progress', 'completed', 'claimed') NOT NULL DEFAULT 'in_progress',
    version INT UNSIGNED NOT NULL DEFAULT 0,
    started_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    completed_at DATETIME(6) NULL,
    claimed_at DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (steam_id64, task_id, period_key),
    KEY idx_launcher_player_task_state (steam_id64, state, updated_at),
    KEY idx_launcher_player_task_definition (task_id, period_key, state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_player_task_distinct_value (
    steam_id64 BIGINT UNSIGNED NOT NULL,
    task_id BIGINT UNSIGNED NOT NULL,
    period_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    distinct_key VARCHAR(160) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    first_seen_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (steam_id64, task_id, period_key, distinct_key),
    KEY idx_launcher_player_task_distinct_definition (task_id, period_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_player_task_group_state (
    steam_id64 BIGINT UNSIGNED NOT NULL,
    group_id BIGINT UNSIGNED NOT NULL,
    period_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state ENUM('in_progress', 'completed', 'claimed') NOT NULL DEFAULT 'in_progress',
    completed_at DATETIME(6) NULL,
    claimed_at DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (steam_id64, group_id, period_key),
    KEY idx_launcher_player_task_group_state (steam_id64, state, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_task_claim (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    steam_id64 BIGINT UNSIGNED NOT NULL,
    task_id BIGINT UNSIGNED NULL,
    group_id BIGINT UNSIGNED NULL,
    period_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status ENUM('pending', 'granted', 'failed') NOT NULL DEFAULT 'pending',
    reward_snapshot JSON NOT NULL COMMENT '领取时冻结奖励，防止后台修改定义影响重试',
    error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    granted_at DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_launcher_task_claim_idempotency (idempotency_key),
    UNIQUE KEY uk_launcher_task_claim_task (steam_id64, task_id, period_key),
    UNIQUE KEY uk_launcher_task_claim_group (steam_id64, group_id, period_key),
    KEY idx_launcher_task_claim_retry (status, updated_at),
    CONSTRAINT chk_launcher_task_claim_owner CHECK ((task_id IS NULL) <> (group_id IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS launcher_task_event_inbox (
    event_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    steam_id64 BIGINT UNSIGNED NOT NULL,
    metric_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    delta_value BIGINT NOT NULL DEFAULT 1 COMMENT '允许后台用负数事件修正误计进度；最终进度必须钳制为非负数',
    distinct_key VARCHAR(160) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    dimensions_json JSON NULL,
    occurred_at DATETIME(6) NOT NULL,
    processed_at DATETIME(6) NULL,
    processing_error VARCHAR(255) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (event_id),
    KEY idx_launcher_task_event_pending (processed_at, created_at),
    KEY idx_launcher_task_event_player (steam_id64, metric_key, occurred_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Compatibility catalogue for the tasks that remain hard-coded in the
-- existing season-pass game implementation. Rewards are intentionally not
-- duplicated here because the legacy implementation remains their authority.
INSERT INTO launcher_task_campaign
    (code, kind, title, description, status, sort_order, metadata_json)
VALUES
    ('legacy-season-pass', 'season', '赛季通行证', '由现有 season_pass_* 表提供进度和领取状态。', 'published', 900,
     JSON_OBJECT('provider', 'season_pass', 'progressAuthority', 'legacy', 'rewardAuthority', 'legacy'))
ON DUPLICATE KEY UPDATE
    title = VALUES(title), description = VALUES(description), status = VALUES(status),
    sort_order = VALUES(sort_order), metadata_json = VALUES(metadata_json);

SET @launcher_legacy_campaign_id = (
    SELECT id FROM launcher_task_campaign WHERE code = 'legacy-season-pass' LIMIT 1
);

INSERT INTO launcher_task_group
    (campaign_id, code, category, title, description, repeat_policy, completion_rule, unlock_policy, enabled, sort_order)
VALUES
    (@launcher_legacy_campaign_id, 'daily-login', 'daily', '每日登录', '每天登录一次。', 'daily', 'all', 'parallel', 1, 10),
    (@launcher_legacy_campaign_id, 'daily-games', 'daily', '每日游玩', '完成当日对局里程碑。', 'daily', 'all', 'parallel', 1, 20),
    (@launcher_legacy_campaign_id, 'daily-online', 'daily', '每日在线', '达到当日在线时长里程碑。', 'daily', 'all', 'parallel', 1, 30),
    (@launcher_legacy_campaign_id, 'weekly-login', 'weekly', '每周登录', '本周至少登录一次。', 'weekly', 'all', 'parallel', 1, 10),
    (@launcher_legacy_campaign_id, 'weekly-games', 'weekly', '每周游玩', '完成本周对局里程碑。', 'weekly', 'all', 'parallel', 1, 20),
    (@launcher_legacy_campaign_id, 'weekly-modes', 'weekly', '模式探索', '体验不同的游戏模式。', 'weekly', 'all', 'parallel', 1, 30)
ON DUPLICATE KEY UPDATE
    category = VALUES(category), title = VALUES(title), description = VALUES(description),
    repeat_policy = VALUES(repeat_policy), enabled = VALUES(enabled), sort_order = VALUES(sort_order);

INSERT INTO launcher_task_definition
    (group_id, code, revision, series_code, title, description, progress_source, metric_key,
     aggregation, target_value, unit, claim_policy, enabled, sort_order, published_at)
SELECT g.id, seed.code, 1, seed.series_code, seed.title, seed.description, 'legacy', seed.metric_key,
       seed.aggregation, seed.target_value, seed.unit, 'external', 1, seed.sort_order, CURRENT_TIMESTAMP(6)
FROM launcher_task_group AS g
JOIN (
    SELECT 'daily-login' group_code, 'login-1' code, 'login' series_code, '每日登录' title, '今日登录 1 次。' description, 'daily_logged_in' metric_key, 'boolean' aggregation, 1 target_value, '次' unit, 10 sort_order
    UNION ALL SELECT 'daily-games', 'games-1', 'games', '完成 1 局', '今日完成 1 局游戏。', 'daily_games', 'count', 1, '局', 10
    UNION ALL SELECT 'daily-games', 'games-3', 'games', '完成 3 局', '今日完成 3 局游戏。', 'daily_games', 'count', 3, '局', 20
    UNION ALL SELECT 'daily-games', 'games-5', 'games', '完成 5 局', '今日完成 5 局游戏。', 'daily_games', 'count', 5, '局', 30
    UNION ALL SELECT 'daily-online', 'online-10', 'online', '在线 10 分钟', '今日累计在线 10 分钟。', 'daily_online_minutes', 'sum', 10, '分钟', 10
    UNION ALL SELECT 'daily-online', 'online-30', 'online', '在线 30 分钟', '今日累计在线 30 分钟。', 'daily_online_minutes', 'sum', 30, '分钟', 20
    UNION ALL SELECT 'daily-online', 'online-60', 'online', '在线 60 分钟', '今日累计在线 60 分钟。', 'daily_online_minutes', 'sum', 60, '分钟', 30
    UNION ALL SELECT 'weekly-login', 'login-1', 'login', '每周登录', '本周登录 1 次。', 'weekly_logged_in', 'boolean', 1, '次', 10
    UNION ALL SELECT 'weekly-games', 'games-1', 'games', '完成 1 局', '本周完成 1 局游戏。', 'weekly_games', 'count', 1, '局', 10
    UNION ALL SELECT 'weekly-games', 'games-5', 'games', '完成 5 局', '本周完成 5 局游戏。', 'weekly_games', 'count', 5, '局', 20
    UNION ALL SELECT 'weekly-games', 'games-10', 'games', '完成 10 局', '本周完成 10 局游戏。', 'weekly_games', 'count', 10, '局', 30
    UNION ALL SELECT 'weekly-modes', 'modes-3', 'modes', '体验 3 种模式', '本周体验 3 种不同模式。', 'weekly_completed_modes', 'distinct', 3, '种模式', 10
) AS seed ON seed.group_code = g.code
WHERE g.campaign_id = @launcher_legacy_campaign_id
ON DUPLICATE KEY UPDATE
    title = VALUES(title), description = VALUES(description), metric_key = VALUES(metric_key),
    aggregation = VALUES(aggregation), target_value = VALUES(target_value), unit = VALUES(unit),
    claim_policy = VALUES(claim_policy), enabled = VALUES(enabled), sort_order = VALUES(sort_order);

INSERT INTO launcher_task_legacy_binding
    (task_id, provider, season_id, legacy_scope, legacy_quest_key, progress_metric, claimed_status_min)
SELECT d.id, 'season_pass', 0, seed.legacy_scope, seed.quest_key, seed.progress_metric, 2
FROM launcher_task_definition AS d
JOIN launcher_task_group AS g ON g.id = d.group_id
JOIN (
    SELECT 'daily-login' group_code, 'login-1' task_code, 'daily' legacy_scope, '1' quest_key, 'daily_logged_in' progress_metric
    UNION ALL SELECT 'daily-games', 'games-1', 'daily', '2', 'daily_games'
    UNION ALL SELECT 'daily-games', 'games-3', 'daily', '3', 'daily_games'
    UNION ALL SELECT 'daily-games', 'games-5', 'daily', '4', 'daily_games'
    UNION ALL SELECT 'daily-online', 'online-10', 'daily', '5', 'daily_online_minutes'
    UNION ALL SELECT 'daily-online', 'online-30', 'daily', '6', 'daily_online_minutes'
    UNION ALL SELECT 'daily-online', 'online-60', 'daily', '7', 'daily_online_minutes'
    UNION ALL SELECT 'weekly-login', 'login-1', 'weekly', '101', 'weekly_logged_in'
    UNION ALL SELECT 'weekly-games', 'games-1', 'weekly', '102', 'weekly_games'
    UNION ALL SELECT 'weekly-games', 'games-5', 'weekly', '103', 'weekly_games'
    UNION ALL SELECT 'weekly-games', 'games-10', 'weekly', '104', 'weekly_games'
    UNION ALL SELECT 'weekly-modes', 'modes-3', 'weekly', '105', 'weekly_completed_modes'
) AS seed ON seed.group_code = g.code AND seed.task_code = d.code
WHERE g.campaign_id = @launcher_legacy_campaign_id AND d.revision = 1
ON DUPLICATE KEY UPDATE
    provider = VALUES(provider), season_id = VALUES(season_id), legacy_scope = VALUES(legacy_scope),
    legacy_quest_key = VALUES(legacy_quest_key), progress_metric = VALUES(progress_metric),
    claimed_status_min = VALUES(claimed_status_min);

SET @launcher_legacy_campaign_id = NULL;
