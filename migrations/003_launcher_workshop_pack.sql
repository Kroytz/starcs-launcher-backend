CREATE TABLE IF NOT EXISTS launcher_workshop_pack (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    kind ENUM('base', 'mode') NOT NULL COMMENT 'base=所有模式公共资源，mode=指定模式资源',
    mode VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'ALL' COMMENT '基础包固定为 ALL；模式包使用 TTT/SCP/JB/ZM 等代码',
    title VARCHAR(100) NOT NULL,
    description VARCHAR(255) NOT NULL DEFAULT '',
    workshop_id BIGINT UNSIGNED NOT NULL,
    enabled TINYINT(1) UNSIGNED NOT NULL DEFAULT 1,
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_launcher_workshop_pack (kind, mode, workshop_id),
    KEY idx_launcher_workshop_pack_lookup (mode, enabled, sort_order),
    CONSTRAINT chk_launcher_workshop_pack_scope CHECK (
        (kind = 'base' AND mode = 'ALL') OR
        (kind = 'mode' AND mode <> 'ALL')
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO launcher_workshop_pack
    (kind, mode, title, description, workshop_id, enabled, sort_order)
VALUES
    ('base', 'ALL', 'StarCS 基础资源包', '所有模式建议订阅的公共资源', 3711721516, 1, 10),
    ('mode', 'TTT', '匪镇谍影资源包', '匪镇谍影模式资源', 3652681776, 1, 20),
    ('mode', 'SCP', '收容失效资源包', '收容失效模式资源', 3652674769, 1, 20),
    ('mode', 'JB', '监狱风云资源包', '监狱风云模式资源', 3248777705, 1, 20),
    ('mode', 'ZM', '生化感染资源包', '生化感染模式资源', 3327753176, 1, 20)
ON DUPLICATE KEY UPDATE
    title = VALUES(title),
    description = VALUES(description),
    enabled = VALUES(enabled),
    sort_order = VALUES(sort_order),
    updated_at = CURRENT_TIMESTAMP(6);
