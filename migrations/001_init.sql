CREATE TABLE IF NOT EXISTS `system_metadata` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `revision` BIGINT NOT NULL,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `unique_revision` (`revision`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `vips` (
  `id` CHAR(36) NOT NULL,
  `vip` VARCHAR(45) NOT NULL,
  `port` INT NOT NULL,
  `protocol` ENUM('TCP', 'UDP') NOT NULL,
  `lb_method` ENUM('maglev') NOT NULL DEFAULT 'maglev',
  `encap_type` ENUM('GRE4', 'GRE6', 'L3DSR', 'NAT4', 'NAT6') NULL,
  `dscp` TINYINT UNSIGNED NULL,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `unique_vip_port_protocol` (`vip`, `port`, `protocol`),
  KEY `idx_vips_vip_protocol_port` (`vip`, `protocol`, `port`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `health_checks` (
  `id` CHAR(36) NOT NULL,
  `vip_id` CHAR(36) NOT NULL,
  `type` ENUM('http', 'https', 'tcp', 'ping', 'tls-hello') NOT NULL,
  `interval_sec` INT NOT NULL DEFAULT 5,
  `timeout_sec` INT NOT NULL DEFAULT 3,
  `rise_count` INT NOT NULL DEFAULT 3,
  `fall_count` INT NOT NULL DEFAULT 3,
  `config` JSON,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `unique_health_checks_vip_id` (`vip_id`),
  CONSTRAINT `fk_health_checks_vip_id` FOREIGN KEY (`vip_id`) REFERENCES `vips` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `backends` (
  `id` CHAR(36) NOT NULL,
  `vip_id` CHAR(36) NOT NULL,
  `ip` VARCHAR(45) NOT NULL,
  `weight` INT NOT NULL DEFAULT 1,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `unique_vip_backend` (`vip_id`, `ip`),
  KEY `idx_backends_vip_id_ip` (`vip_id`, `ip`),
  CONSTRAINT `fk_backends_vip_id` FOREIGN KEY (`vip_id`) REFERENCES `vips` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `change_log` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `revision` BIGINT NOT NULL,
  `event_type` ENUM('vip_created', 'vip_updated', 'vip_deleted', 'backend_added', 'backend_updated', 'backend_deleted') NOT NULL,
  `vip_id` CHAR(36) NULL,
  `backend_id` CHAR(36) NULL,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_change_log_revision` (`revision`),
  KEY `idx_change_log_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `schema_migrations` (
  `version` VARCHAR(255) NOT NULL,
  `applied_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO `system_metadata` (`revision`) VALUES (1) ON DUPLICATE KEY UPDATE `revision` = `revision`;
