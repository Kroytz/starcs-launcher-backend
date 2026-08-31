CREATE TABLE IF NOT EXISTS launcher_release (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    version VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '语义化版本号，例如 0.2.0',
    mandatory TINYINT(1) UNSIGNED NOT NULL DEFAULT 0 COMMENT '1=强制更新：所有低于该版本的客户端启动时自动更新',
    changelog MEDIUMTEXT NOT NULL COMMENT 'markdown 格式的更新日志',
    artifact_url VARCHAR(512) NOT NULL COMMENT 'static.starcs.cn 上的 NSIS -setup.exe 地址',
    signature TEXT NOT NULL COMMENT 'minisign .sig 文件内容（不是 URL）',
    pub_date DATETIME(6) NOT NULL COMMENT '发布时间；最新发布按 pub_date 倒序取',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_launcher_release_version (version),
    KEY idx_launcher_release_pub_date (pub_date DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
