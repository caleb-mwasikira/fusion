--
-- Table structure for table `organizations`
--
DROP TABLE IF EXISTS `organizations`;

CREATE TABLE IF NOT EXISTS `organizations` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(255) NOT NULL,
  `admin_name` VARCHAR(255) NOT NULL,
  `admin_email` VARCHAR(255) NOT NULL,
  `org_password` VARCHAR(255) NOT NULL,
  PRIMARY KEY (`id`)
);


--
-- Table structure for table `users`
--
DROP TABLE IF EXISTS `users`;

CREATE TABLE IF NOT EXISTS `users` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `username` VARCHAR(255) NOT NULL,
  `email` VARCHAR(255) NOT NULL UNIQUE,
  `password` VARCHAR(255) NOT NULL,
  `org_name` VARCHAR(255) NOT NULL,
  `dept_name` VARCHAR(255) NOT NULL,
  PRIMARY KEY (`id`)
);

--
-- Table structure for table `password_reset_tokens`
--
DROP TABLE IF EXISTS `password_reset_tokens`;

CREATE TABLE IF NOT EXISTS `password_reset_tokens` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `email` VARCHAR(255) NOT NULL,
  `otp` VARCHAR(255) NOT NULL UNIQUE,
  `expires_at` DATETIME NOT NULL,
  `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
);

