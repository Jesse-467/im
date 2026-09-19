-- 回滚：头像收窄回 VARCHAR(512)。
-- 超长数据（base64 头像）会被截断，仅用于本地开发回退。

ALTER TABLE account_user
    ALTER COLUMN avatar_url TYPE VARCHAR(512);
