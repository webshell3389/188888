-- MockC2 MySQL 初始化（容器首次启动自动执行）
-- 库与用户已由 compose 环境变量创建；这里补字符集兜底与最小权限收敛。

ALTER DATABASE mockc2 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- 应用用户仅需本库 DML/DDL（迁移需要 CREATE/ALTER/INDEX）
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES
  ON mockc2.* TO 'mockc2'@'%';
FLUSH PRIVILEGES;
