
--
-- Table structure for table `users`
--

CREATE TABLE IF NOT EXISTS `users`(
  `id` integer primary key autoincrement NOT NULL,
  `username` varchar(255) NOT NULL,
  `password` varchar(255) NOT NULL,
  `org_name` varchar(255) NOT NULL,
  `dept_name` varchar(255) NOT NULL
);
