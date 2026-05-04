ALTER TABLE `health_checks`
  MODIFY `type` ENUM('http', 'https', 'tcp', 'ping', 'tls-hello') NOT NULL;
