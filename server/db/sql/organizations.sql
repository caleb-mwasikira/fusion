
--
-- Table structure for table `organizations`
--

CREATE TABLE IF NOT EXISTS `organizations`(
  `id` integer primary key autoincrement NOT NULL,
  `name` varchar(255) NOT NULL,
  `admin_id` integer NOT NULL
);

