-- 回滚：按依赖倒序删除全部表
DROP TABLE IF EXISTS message_outbox;
DROP TABLE IF EXISTS friend_request;
DROP TABLE IF EXISTS friend_relation;
DROP TABLE IF EXISTS message;
DROP TABLE IF EXISTS conversation_member;
DROP TABLE IF EXISTS conversation;
