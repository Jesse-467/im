# IM Message · 接口索引与业务链路

> 本文档是接口的唯一索引入口：先按**业务功能**分类导航，再给出每个接口的
> 请求/响应细节，最后逐条说明其**业务逻辑与调用链路**。
>
> 配套文档：[README.md](file:///d:/GoCode/IMmessage/im/README.md)（架构与部署）、
> [REFACTOR_PLAN.md](file:///d:/GoCode/IMmessage/im/REFACTOR_PLAN.md)（重构决策记录）。

---

## 目录

- [一、按业务功能分类的接口索引](#一按业务功能分类的接口索引)
  - [1. 账号功能（Account 服务）](#1-账号功能account-服务)
  - [2. 好友功能](#2-好友功能)
  - [3. 会话功能](#3-会话功能)
  - [4. 单聊与消息功能](#4-单聊与消息功能)
  - [5. 群聊功能](#5-群聊功能)
  - [6. 实时通信（WebSocket）](#6-实时通信websocket)
  - [7. 运维探针](#7-运维探针)
- [二、接口详情](#二接口详情)
  - [0. 公共约定](#0-公共约定)
  - [账号功能](#账号功能)
  - [好友功能](#好友功能)
  - [会话功能](#会话功能)
  - [消息功能](#消息功能)
  - [群聊功能](#群聊功能)
  - [WebSocket](#websocket)
  - [运维探针](#运维探针)
- [三、业务逻辑与链路解析](#三业务逻辑与链路解析)
  - [3.1 账号功能（含令牌校验模式与多设备在线上限）](#31-账号功能)
  - [3.2 好友功能](#32-好友功能)
  - [3.3 会话功能](#33-会话功能)
  - [3.4 消息功能](#34-消息功能)
  - [3.5 群聊功能](#35-群聊功能)
  - [3.6 WebSocket 实时通信](#36-websocket-实时通信)
  - [3.7 配置装载与分级校验](#37-配置装载与分级校验)
- [四、附录](#四附录)

---

## 一、按业务功能分类的接口索引

> 服务地址：Account `http://<host>:8001`，Chat `http://<host>:8002`。
> 除标注「否」外，全部业务接口需要携带 `Authorization: Bearer <token>`。

### 1. 账号功能（Account 服务）

账号中心是**独立微服务**，是所有聊天账号的来源。Chat 通过 gRPC 消费它的能力。

| 功能 | 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- | --- |
| 注册 | POST | `/api/user/register` | 否 | 邮箱 + 密码创建账号 |
| 登录 | POST | `/api/user/login` | 否 | 校验凭证并签发 JWT，受多设备在线上限约束 |
| 登出 | POST | `/api/user/logout` | 是 | 吊销当前令牌并释放设备在线位 |
| 查本人资料 | POST | `/api/user/personal_info` | 是 | 昵称 / 性别 / 头像 / 邮箱 |
| 查他人资料 | POST | `/api/user/query_user_info` | 是 | 按 `userId` 查询 |
| 改密码 | POST | `/api/user/reset_password` | 是 | 需校验旧密码，成功后吊销全部令牌 |
| 改资料 | POST | `/api/user/modify_personal_info` | 是 | 昵称 / 性别 / 头像 |

→ 详情：[注册](#post-apiuserregister) · [登录](#post-apiuserlogin) ·
[登出](#post-apiuserlogout) ·
[本人资料](#post-apiuserpersonal_info) · [他人资料](#post-apiuserquery_user_info) ·
[改密码](#post-apiuserreset_password) · [改资料](#post-apiusermodify_personal_info)

> **令牌校验有两种模式**（`TOKEN_MODE`）：`self` 只验证 JWT 自身，
> `db` 还要求数据库中该令牌未被吊销。多设备踢下线依赖 `db` 模式即时生效，
> 详见 [3.1 令牌校验模式](#令牌校验模式token_mode)。

### 2. 好友功能

| 功能 | 方法 | 路径 | 说明 |
| --- | --- | --- | --- |
| 发起好友申请 | POST | `/api/group/add_friend` | 已好友时直接返回标记，不重复建申请 |
| 处理好友申请 | POST | `/api/group/handle_friend` | 同意后自动建立单聊会话 |
| 好友列表 | POST | `/api/friend/list` | 含备注名与对方昵称头像 |
| 好友申请列表 | POST | `/api/friend/request_list` | 可按状态过滤 |

→ 详情：[发起申请](#post-apigroupadd_friend) · [处理申请](#post-apigrouphandle_friend) ·
[好友列表](#post-apifriendlist) · [申请列表](#post-apifriendrequest_list)

### 3. 会话功能

| 功能 | 方法 | 路径 | 说明 |
| --- | --- | --- | --- |
| 会话列表 | POST | `/api/group/message_group_info_list` | 消息页首屏，含未读数与最后一条消息 |
| 上报已读 | POST | `/api/group/mark_read` | 推进已读位点并同步扣减未读数 |

→ 详情：[会话列表](#post-apigroupmessage_group_info_list) · [上报已读](#post-apigroupmark_read)

### 4. 单聊与消息功能

| 功能 | 方法 | 路径 | 说明 |
| --- | --- | --- | --- |
| 发送消息（HTTP） | POST | `/api/message/upload` | 与 WS 发送共用同一套业务规则 |
| 拉取消息 | POST | `/api/message/pull` | 按 seq 区间取，支持升/降序 |
| 增量同步 | POST | `/api/message/sync` | 等价 `pull(ascending=true)`，供重连补洞 |
| 撤回消息 | POST | `/api/message/recall` | 仅发送者本人可撤回 |

→ 详情：[发送消息](#post-apimessageupload) · [拉取消息](#post-apimessagepull) ·
[增量同步](#post-apimessagesync) · [撤回消息](#post-apimessagerecall)

### 5. 群聊功能

| 功能 | 方法 | 路径 | 说明 |
| --- | --- | --- | --- |
| 创建群聊 | POST | `/api/group/create_group_chat` | 创建者为群主 |
| 拉人入群 | POST | `/api/group/add_group_chat` | 仅群成员可操作 |
| 群成员 ID 列表 | POST | `/api/group/group_user_list` | 仅返回用户 ID |
| 群成员详情 | POST | `/api/group/member_list` | 含昵称、头像、角色 |
| 退出群聊 | POST | `/api/group/quit` | 群主不能退出 |

→ 详情：[创建群聊](#post-apigroupcreate_group_chat) · [拉人入群](#post-apigroupadd_group_chat) ·
[成员 ID 列表](#post-apigroupgroup_user_list) · [成员详情](#post-apigroupmember_list) ·
[退出群聊](#post-apigroupquit)

### 6. 实时通信（WebSocket）

| 功能 | 协议 | 路径 | 说明 |
| --- | --- | --- | --- |
| 长连接 | WS | `/ws?token=<jwt>` | 上行发送消息、下行接收推送 |

→ 详情：[WebSocket 协议](#websocket)

### 7. 运维探针

| 方法 | 路径 | 服务 | 说明 |
| --- | --- | --- | --- |
| GET | `/healthz` | Account / Chat | 存活探针，不触碰外部依赖 |
| GET | `/readyz` | Account / Chat | 就绪探针，真实探测 DB 与 Redis |

→ 详情：[运维探针](#运维探针)

---

## 二、接口详情

### 0. 公共约定

#### 请求头

| 头 | 必填 | 说明 |
| --- | --- | --- |
| `Content-Type` | 是 | 固定 `application/json` |
| `Authorization` | 视接口 | `Bearer <accessToken>`，业务接口均需要 |
| `platform` | 否 | WebSocket 握手时标识客户端平台（`web` / `ios` / `android`），缺省为 `unknown` |

#### 统一响应结构

```json
{ "code": 0, "msg": "OK", "data": {} }
```

> **HTTP 状态码约定（重要）**：业务接口的成功与失败**都返回 HTTP 200**，
> 真实结果由响应体的 `code` 表达。客户端必须判断 `code`，不能只看 HTTP 状态码。
>
> 例外：`/readyz` 未就绪时返回 HTTP `503`；WebSocket 握手鉴权失败返回 HTTP `401`。

#### 错误码

| code | 含义 | 典型场景 |
| --- | --- | --- |
| `0` | 成功 | — |
| `4001` | 身份认证失败 | 缺少令牌、令牌过期或签名不符 |
| `4002` | 参数校验失败 | 字段缺失、长度越界、ID 非法 |
| `4003` | 数据不存在 | 会话 / 消息 / 群聊不存在 |
| `4004` | 数据已存在 | 重复注册、已是好友、重复入群 |
| `4005` | 操作过于频繁 | 触发限流 |
| `4006` | 没有操作权限 | 非会话成员、非群主、非好友 |
| `5000` | 服务繁忙 | 未预期的内部错误 |
| `5001` | 数据操作异常 | 数据库故障 |
| `5002` | 缓存操作异常 | Redis 故障 |
| `5004` | 消息投递异常 | 消息队列故障 |

> `4003` / `4004` / `4006` 的具体文案由服务端按场景给出，
> 例如 `4003` 可能是「会话不存在」，也可能是「消息不存在」。

#### 大整数 ID 约定（**重要**）

会话 ID、消息 ID、用户 ID 都是 **19 位雪花值**，超出 JavaScript 的
`Number.MAX_SAFE_INTEGER`（16 位）。因此：

- **服务端下发的 ID 一律是 JSON 字符串**（如 `"359572627845451776"`）；
- **客户端回传时请原样使用字符串**，不要转成 `Number`；
- 服务端**同时接受**数字与字符串两种字面量，因此老客户端传数字仍可工作。

若客户端把 ID 当数字解析，`JSON.parse` 会静默把它舍入成另一个值
（`...784` 变成 `...800`），且没有任何异常可捕获，只会在后续请求中
表现为「会话不存在」或「数据为空」。

> 例外：`seq`（会话内序号）、`unreadCount`、`createTime` 等不是雪花值，
> 保持数字类型，便于客户端直接做算术比较。

#### 会话标识的两种写法

请求中的会话标识支持两个字段，服务端按 `conversationId` → `groupId` 的顺序解析：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `conversationId` | 字符串或数字 | **推荐**。会话主键，直接使用 |
| `groupId` | 字符串 | 兼容旧客户端。单聊为 `S:小uid:大uid`，群聊为 `G:会话ID` |

> `conversationId` 也是数字字符串（如 `"359572627845451776"`），
> 与 `groupId` 的区别是它不做任何业务解析。

---
### 账号功能

账号中心的接口由 Account 服务提供（默认 `:8001`）。

#### POST api-user-register

`POST /api/user/register` —— 注册新账号。邮箱与密码为必填。

| 参数 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `email` | string | 是 | 合法邮箱格式 | 服务端统一转小写并去空格 |
| `password` | string | 是 | 长度 6~32 | 明文传输，落库前 bcrypt 哈希 |
| `nickName` | string | 否 | — | 留空时随机生成 8 位昵称 |
| `gender` | int | 否 | `0` 未知 / `1` 男 / `2` 女 | 默认 `0` |

**响应**

```json
{ "code": 0, "msg": "OK", "data": {} }
```

> 与既有接口保持一致：注册成功**不返回**用户数据。客户端需要 `userId` 时请调登录。

**错误**

| code | 场景 |
| --- | --- |
| `4002` | 邮箱格式非法、密码长度不符、性别取值非法 |
| `4004` | 邮箱已被注册 |

#### POST api-user-login

`POST /api/user/login` —— 校验凭证并签发 JWT，同时登记设备在线位。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `email` | string | 是 | 注册时使用的邮箱 |
| `password` | string | 是 | 明文密码 |
| `deviceId` | string | 否 | 设备标识，客户端应本地持久化（如首次启动生成的 UUID）。同一设备重新登录不会占用新的在线位 |
| `platform` | string | 否 | 平台标识（`web` / `ios` / `android`），缺省 `unknown`，仅用于展示与排障 |

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "userId": 38,
    "accessToken": "eyJhbGciOiJIUzI1NiIs...",
    "accessExpire": 1789880990,
    "evictedDevices": ["d:old-device"]
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `userId` | number | 用户 ID |
| `accessToken` | string | 后续请求放入 `Authorization: Bearer <token>` |
| `accessExpire` | number | 过期时间，**Unix 秒** |
| `evictedDevices` | string[] | 本次登录因超出设备上限被踢下线的设备标识。**无设备被踢时该字段不出现** |

> `userId` 当前是自增整数，不是雪花值；接入历史数据后可能变长，
> 客户端仍建议按字符串容错处理。

> `evictedDevices` 里的标识形态为 `d:<deviceId>`（客户端上报了 `deviceId`）
> 或 `j:<jti>`（未上报），后者表示服务端按令牌区分设备。

**错误**

| code | 场景 |
| --- | --- |
| `4001` | 邮箱或密码错误（刻意不区分，避免账号枚举） |
| `4002` | 参数缺失或格式非法 |

#### POST api-user-logout

`POST /api/user/logout` —— 吊销当前令牌并释放其占用的设备在线位。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `accessToken` | string | 二选一 | 待吊销的令牌 |
| `deviceId` | string | 否 | 与登录时保持一致，用于释放该设备占用的在线位 |

> 令牌也可以放在 `Authorization: Bearer` 头里（与请求体二选一）。
> 请求体为空时仅靠头部令牌也能正常工作。

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "success": true } }
```

**幂等性**

携带的令牌已经无效（过期 / 已被吊销）时**仍返回成功**。
这样客户端在令牌自然过期后调登出不会拿到错误、被迫忽略它。

**错误**

| code | 场景 |
| --- | --- |
| `4001` | 既没有请求体 `accessToken`，也没有 `Authorization` 头 |
| `5000` | 吊销时存储故障 |

#### POST api-user-personal_info

`POST /api/user/personal_info` —— 查询本人资料。不接受参数，身份取自 JWT。

**请求**

```json
{}
```

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "userId": 38,
    "nickName": "FIX-a",
    "gender": 0,
    "email": "fix_a_12345678@example.com",
    "avatarUrl": "https://s1.ax1x.com/2022/05/25/XFaDqP.png"
  }
}
```

**错误**

| code | 场景 |
| --- | --- |
| `4001` | 未携带令牌 / 令牌无效 |
| `4003` | 用户不存在（账号已注销） |

#### POST api-user-query_user_info

`POST /api/user/query_user_info` —— 按 `userId` 查询他人资料，用于展示对方昵称与头像。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `userId` | number | 是 | 目标用户 ID |

**响应**：同「本人资料」。

**错误**

| code | 场景 |
| --- | --- |
| `4002` | `userId` 缺失 |
| `4003` | 用户不存在 |

#### POST api-user-reset_password

`POST /api/user/reset_password` —— 修改密码。需校验旧密码，因此必须携带有效令牌。

| 参数 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `email` | string | 是 | 合法邮箱 | 账号邮箱 |
| `oldPassword` | string | 是 | 长度 6~32 | 当前密码 |
| `newPassword` | string | 是 | 长度 6~32 | 新密码 |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "success": true } }
```

**错误**

| code | 场景 |
| --- | --- |
| `4001` | 旧密码不正确 |
| `4002` | 新密码长度不符 |
| `4003` | 邮箱对应的账号不存在 |

#### POST api-user-modify_personal_info

`POST /api/user/modify_personal_info` —— 修改昵称 / 性别 / 头像。字段均可选，只传需要改的。

| 参数 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `nickName` | string | 否 | 长度 ≤ 32 | 留空表示不修改 |
| `gender` | int | 否 | `0` / `1` / `2` | 需与现有值不同才生效 |
| `avatarUrl` | string | 否 | — | 头像地址 |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "success": true } }
```

**错误**

| code | 场景 |
| --- | --- |
| `4001` | 未认证 |
| `4002` | 昵称超长、性别取值非法 |

---


### 好友功能

#### POST api-group-add_friend

`POST /api/group/add_friend` —— 发起好友申请。若双方已是好友，直接返回
`alreadyFriends=true`，不产生新申请。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `userId` | 字符串或数字 | 是 | 要添加的目标用户 ID |
| `user_id` | 字符串或数字 | 否 | 兼容旧客户端的历史字段名 |
| `applyMsg` | string | 否 | 申请附言，长度 ≤ 100 |

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "requestId": "12345",
    "alreadyFriends": false
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `requestId` | 申请 ID，后续调 `handle_friend` 时回传 |
| `alreadyFriends` | 为 `true` 时表示已是好友，`requestId` 为 `"0"` |

**错误**

| code | 场景 |
| --- | --- |
| `4002` | `userId` 缺失或非法、附言超长 |
| `4004` | 不能添加自己 |

> 重复点击申请不会产生多条记录：已有待处理申请时服务端直接复用。
> 已过期的旧申请会先置为「已过期」，把待处理唯一键让出来。

#### POST api-group-handle_friend

`POST /api/group/handle_friend` —— 处理（同意 / 拒绝）收到的好友申请。
**注意字段名是 `isAgree`**。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `requestId` | 字符串或数字 | 二选一 | 申请 ID。推荐使用 |
| `groupId` | string | 二选一 | 旧契约：单聊会话标识，服务端据此反查待处理申请 |
| `isAgree` | bool | 是 | `true` 同意，`false` 拒绝 |

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "conversationId": "359572627845451776",
    "groupId": "359572627845451776"
  }
}
```

> 同意时返回新建（或复用）的单聊会话 ID；拒绝时 `conversationId` 为 `"0"`。

**错误**

| code | 场景 |
| --- | --- |
| `4003` | 申请不存在 |
| `4004` | 申请已被处理过（并发重复提交） |
| `4006` | 不是该申请的收件人 |

#### POST api-friend-list

`POST /api/friend/list` —— 查询好友列表，含备注名与对方昵称头像。不接受参数。

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "list": [
      { "userId": "39", "nickName": "FIX-b", "avatarUrl": "https://...", "remark": "同事" }
    ]
  }
}
```

> 对方资料取不到时会降级返回（昵称头像为空），不会让整个列表失败。

#### POST api-friend-request_list

`POST /api/friend/request_list` —— 查询收到的好友申请。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `status` | int | 否 | `0` 待处理 / `1` 已同意 / `2` 已拒绝 / `3` 已过期。不传表示全部 |
| `limit` | int | 否 | 条数上限，默认 50，最大 200 |

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "list": [
      {
        "id": "12345",
        "fromUid": "38",
        "toUid": "39",
        "applyMsg": "hi",
        "status": 0,
        "createdAt": 1789794163067,
        "handledAt": 0
      }
    ]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `id` | 申请 ID，回传给 `handle_friend` |
| `fromUid` / `toUid` | 申请人 / 收件人 |
| `status` | 见上表 |
| `createdAt` / `handledAt` | 毫秒时间戳，未处理时 `handledAt` 为 `0` |

---

### 会话功能

#### POST api-group-message_group_info_list

`POST /api/group/message_group_info_list` —— 消息页首屏。返回当前用户参与的
全部会话，按最近活跃时间倒序。不接受参数。

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "list": [
      {
        "conversationId": "359572627845451776",
        "groupId": "359572627845451776",
        "type": 1,
        "name": "",
        "aliasName": "FIX-b",
        "avatarUrl": "https://...",
        "unreadCount": 2,
        "lastReadSeq": 3,
        "maxSeq": 5,
        "lastMsg": {
          "id": "359572630000000001",
          "conversationId": "359572627845451776",
          "groupId": "359572627845451776",
          "seq": 5,
          "senderId": "38",
          "type": 1,
          "content": "hello",
          "uuid": "cmid-xxx",
          "createTime": 1789795490355
        }
      }
    ]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `type` | `1` 单聊 / `2` 群聊 |
| `name` | 群名；单聊为空 |
| `aliasName` | 展示名：单聊为对方昵称或备注，群聊为群名 |
| `unreadCount` | 未读数，独立维护的计数 |
| `lastReadSeq` | 我的已读位点 |
| `maxSeq` | 会话最新序号 |
| `lastMsg` | 最后一条消息，无消息时为 `null` |

> **补齐离线消息的方法**：比较每个会话的 `maxSeq` 与 `lastReadSeq`，
> 对落后的会话调 [`/api/message/sync`](#post-apimessagesync)。

#### POST api-group-mark_read

`POST /api/group/mark_read` —— 上报已读位点，服务端同步扣减未读数。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `seq` | number | 是 | 已读到的会话内序号 |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "success": true } }
```

> 位点只能前进不能回退：传一个比当前更小的值不会报错，但也不会生效。

---


### 消息功能

#### POST api-message-upload

`POST /api/message/upload` —— 通过 HTTP 发送消息。与 WebSocket 上行发送
**共用同一套业务规则**，因此校验、幂等、发号、投递行为完全一致。

| 参数 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | — | 会话 ID |
| `groupId` | string | 二选一 | — | 兼容旧字段 |
| `type` | number | **是** | `1`~`5` | `1` 文本 / `2` 图片 / `3` 视频 / `4` 音频 / `5` 系统 |
| `content` | string | 是 | 长度 ≤ 4096 字符 | 消息正文 |
| `clientMsgId` | string | 否 | 长度 ≤ 64 | 幂等键。不传时服务端生成 |
| `uuid` | string | 否 | 长度 ≤ 64 | 兼容旧字段名，语义同 `clientMsgId` |
| `extra` | string | 否 | — | 扩展信息（富媒体元数据等），JSON 字符串 |

> `type` **没有默认值**：缺省时为 `0`，会被判为非法并返回 `4002`。
> 客户端必须在每次发送时显式传 `1`~`5`。

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "id": "359572630000000001",
    "conversationId": "359572627845451776",
    "groupId": "359572627845451776",
    "seq": 6,
    "createTime": 1789795490355,
    "duplicated": false
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `id` | 消息 ID |
| `seq` | 会话内序号，客户端据此排序与补洞 |
| `duplicated` | `true` 表示命中幂等键、返回的是已存在的消息 |

> **客户端应如何用幂等键**：为每条消息生成一个稳定的 `clientMsgId`
> （如 UUID）。超时重试时复用同一个值，服务端会返回原消息而不是新建一条。

**错误**

| code | 场景 |
| --- | --- |
| `4002` | 正文超长、消息类型非法或缺失、幂等键超长 |
| `4003` | 会话不存在 |
| `4006` | 不是该会话的成员 |
| `5000` | 序号分配失败（Redis 不可用） |

> 序号分配失败返回的是 `5000` 而不是 `5002`：该错误在 data 层未携带业务错误码，
> 到 service 层被统一收敛为「服务繁忙」。`5002` 专用于缓存层面能明确识别的异常。

#### POST api-message-pull

`POST /api/message/pull` —— 按序号区间拉取消息。用于历史翻页与离线补齐。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `fromSeq` | number | 否 | **排他**下界，返回 `seq > fromSeq` 的消息。默认 `0` |
| `toSeq` | number | 否 | 上界，返回 `seq <= toSeq`。默认不限 |
| `maxMsgId` | number | 否 | 兼容旧字段，语义等价于 `toSeq` |
| `limit` | number | 否 | 条数上限，默认 20，最大 200 |
| `ascending` | bool | 否 | `true` 升序（补洞用），默认 `false` 降序（翻页用） |
| `platform` | string | 否 | 兼容旧字段，当前不影响结果 |

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "list": [ /* chatMsgDTO，结构见会话列表 */ ],
    "hasMore": true,
    "maxSeq": 42
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `hasMore` | `true` 表示可能还有更多，客户端可继续拉取 |
| `maxSeq` | 会话最新序号，用于判断自己是否落后 |

> `fromSeq` 是**排他**下界：已读到 `seq=5` 时应传 `fromSeq=5`，
> 这样返回的第 6 条开始，不会重复返回已读的那条。

#### POST api-message-sync

`POST /api/message/sync` —— 增量同步。语义上等价于 `pull(ascending=true)`，
单独开路径是为了让「重连补洞」与「历史翻页」两种意图在调用侧区分开。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `fromSeq` | number | 是 | 客户端已读位点，返回它之后的消息 |
| `limit` | number | 否 | 条数上限，默认 20，最大 200 |

**响应**：同 `pull`（`list` 按 seq 升序）。

> `hasMore` 为 `true` 时，以返回的 `maxSeq` 作为新的 `fromSeq` 继续拉取，
> 直到 `hasMore` 为 `false`。

#### POST api-message-recall

`POST /api/message/recall` —— 撤回消息。仅发送者本人可撤回。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `messageId` | 字符串或数字 | 是 | 要撤回的消息 ID |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "success": true } }
```

**错误**

| code | 场景 |
| --- | --- |
| `4003` | 消息不存在 |
| `4004` | 消息不可撤回：不存在、非本人发送、或已被撤回 |
| `4006` | 不是会话成员 |

> 「消息不存在」「非发送者本人」「已被撤回」三种情况**统一返回 `4004`**，
> 服务端不区分。这样既简化了客户端处理，也避免通过错误码泄露
> 「某条消息是否存在」这一信息。

---

### 群聊功能

#### POST api-group-create_group_chat

`POST /api/group/create_group_chat` —— 创建群聊，创建者自动成为群主。

| 参数 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `groupName` | string | 是 | 长度 ≤ 50 | 群名 |
| `memberIds` | array | 否 | 最多 499 个 | 初始成员 ID 列表（不含自己） |
| `toUid` | array | 否 | — | 兼容旧字段名 |

> 「最多 499 个」是因为上限校验统计的是**含创建者在内的总人数**
> （`groupMaxMembers = 500`）。
> 数组元素可混用字符串与数字形式；非法值（`0`、负数）会被自动过滤。

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "conversationId": "359572627845451900",
    "groupId": "359572627845451900",
    "addedCount": 2
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `addedCount` | 实际加入的成员数（不含创建者），便于感知无效成员被跳过 |

**错误**

| code | 场景 |
| --- | --- |
| `4002` | 群名为空或超长、成员数超上限 |

> **创建群聊不校验好友关系**：可以把任意用户直接拉进新群。
> 这是刻意的取舍——建群时逐个校验好友会引入 N 次跨服务调用，
> 且许多产品形态（如拉陌生人进临时群）本就不要求好友关系。
> 若业务上需要限制，应在网关或上游业务层加白名单。

#### POST api-group-add_group_chat

`POST /api/group/add_group_chat` —— 批量拉人入群。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `userIds` | array | 二选一 | 要加入的用户 ID 列表 |
| `toUid` | array | 二选一 | 兼容旧字段名 |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "cnt": 3 } }
```

> `cnt` 是**实际新增**人数：已在群内的会被跳过，不会重复计数。

**错误**

| code | 场景 |
| --- | --- |
| `4003` | 会话不存在 |
| `4006` | 不是群成员 |
| `4002` | 超过群成员上限（500） |

> **当前只校验「操作者是群成员」**，不限制角色，也不校验被拉入者是否为好友。
> 若产品上要求「仅群主可拉人」，需在 biz 层补上角色判断
> （可参考 `RemoveMember` 的写法）。

#### POST api-group-group_user_list

`POST /api/group/group_user_list` —— 查询会话内的用户 ID 列表。
仅返回 ID，适合做批量判断。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "list": ["38", "39", "40"] } }
```

**错误**

| code | 场景 |
| --- | --- |
| `4006` | 不是会话成员（无权查看成员列表） |

#### POST api-group-member_list

`POST /api/group/member_list` —— 查询会话成员详情。比 `group_user_list`
多了昵称、头像与角色。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `withProfile` | bool | 否 | 是否填充昵称头像，默认 `true` |

**响应**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "list": [
      {
        "userId": "38",
        "nickName": "FIX-a",
        "avatarUrl": "https://...",
        "aliasName": "群主备注",
        "role": 2,
        "lastReadSeq": 3,
        "joinedAt": 1789794163067
      }
    ]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `role` | `0` 成员 / `1` 管理员 / `2` 群主 |
| `aliasName` | 该成员在群内的备注名 |
| `lastReadSeq` | 该成员的已读位点（可用于「谁还没看」类需求） |

**错误**

| code | 场景 |
| --- | --- |
| `4003` | 会话不存在 |
| `4006` | 不是会话成员 |

#### POST api-group-quit

`POST /api/group/quit` —— 退出群聊。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `groupId` | string | 二选一 | 兼容旧字段 |

**响应**

```json
{ "code": 0, "msg": "OK", "data": { "success": true } }
```

**错误**

| code | 场景 |
| --- | --- |
| `4006` | **群主不能退出**（需先转让群主或解散群聊） |
| `4003` | 会话不存在 |

> 退群采用**软删除**：成员行保留但标记 `left_at`，因此历史消息不会失去发送者，
> 重新入群也能复用同一行。

---


### WebSocket

#### GET api-ws

`GET /ws` —— 建立长连接。握手时通过**查询参数**传令牌
（浏览器的 WebSocket API 无法自定义请求头）。

```
ws://<host>:8002/ws?token=<accessToken>
```

| 参数 | 位置 | 必填 | 说明 |
| --- | --- | --- | --- |
| `token` | Query | 是 | `accessToken`。也支持放在 `Authorization: Bearer` 头（移动端） |
| `platform` | Header | 否 | 客户端平台标识，缺省 `unknown` |

> **鉴权在升级之前完成**：令牌无效时直接返回 `401`，不会先建立连接再断开。

#### 上行消息

**心跳**

```json
{ "type": "ping" }
```

服务端回 `{"type":"pong","data":<毫秒时间戳>}`。

**发送消息**

```json
{
  "type": "send",
  "conversationId": "359572627845451776",
  "content": "hello",
  "msgType": 1,
  "clientMsgId": "cmid-xxx",
  "extra": "{\"k\":\"v\"}"
}
```

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `type` | string | 是 | 固定 `send` |
| `conversationId` | 字符串或数字 | 二选一 | 会话 ID |
| `conversationIdStr` | string | 二选一 | 与 `conversationId` 等价，供无法用数字表达的客户端使用 |
| `groupId` | string | 二选一 | 兼容旧字段 |
| `content` | string | 是 | 消息正文 |
| `msgType` | number | **是** | `1`~`5`，无默认值 |
| `clientMsgId` | string | 否 | 幂等键 |
| `extra` | string | 否 | 扩展信息 |

> 未知的 `type` **不报错**，只记日志后忽略。这样客户端版本比服务端新时，
> 灰度升级不会被卡住。

#### 下行消息

**新消息推送**

```json
{
  "type": "message",
  "data": {
    "conversationId": "359572627845451776",
    "messageId": "359572630000000001",
    "seq": 6,
    "senderId": "38",
    "type": 1,
    "content": "hello",
    "clientMsgId": "cmid-xxx",
    "createTime": 1789795490355
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `conversationId` | string | 会话 ID |
| `messageId` | string | 消息 ID |
| `seq` | number | 会话内序号 |
| `senderId` | string | 发送者 |
| `type` | number | 消息类型 `1`~`5` |
| `content` | string | 消息正文 |
| `extra` | string | 扩展信息。**为空时不出现**（`omitempty`） |
| `clientMsgId` | string | 幂等键，客户端可用于对应自己发出的消息 |
| `createTime` | number | 毫秒时间戳 |

**错误提示**

```json
{ "type": "error", "data": "错误描述" }
```

**心跳应答**

```json
{ "type": "pong", "data": 1789794590900 }
```

| type | 说明 |
| --- | --- |
| `message` | 新消息 |
| `pong` | 心跳应答 |
| `error` | 上行处理失败的原因 |

#### 连接参数

| 项 | 值 | 说明 |
| --- | --- | --- |
| 服务端 ping 间隔 | 50s | 主动发 WebSocket Ping 帧 |
| pong 超时 | 60s | 超时判定连接已死并清理 |
| 上行单条上限 | 4096 字节 | 超出会被断开 |
| 发送缓冲 | 256 条 | 缓冲满则主动断开该连接 |
| 单条写超时 | 10s | |
| 在线路由 TTL | 90s | 节点心跳 30s，可容忍 2 次丢失 |

#### 发送与接收的对应关系

- **发送者本人也会收到推送**（`type: "message"`），多端登录时其他端同步可见；
- 同一条消息**只会推送一次**，不会既收到回执又收到广播；
- 客户端可用 `messageId` 或 `(conversationId, seq)` 去重，做兜底。

---

### 运维探针

#### GET api-healthz

`GET /healthz` —— 存活探针。只表明进程存活，**不触碰**外部依赖，因此响应极快。

```json
{ "code": 0, "msg": "OK", "data": { "status": "ok", "service": "chat", "env": "dev" } }
```

#### GET api-readyz

`GET /readyz` —— 就绪探针。**真实探测**数据库与缓存，未就绪时返回 HTTP `503`，
供负载均衡摘流。

**就绪**

```json
{
  "code": 0,
  "msg": "OK",
  "data": {
    "ready": true,
    "dependencies": [
      { "name": "postgres", "ok": true },
      { "name": "redis", "ok": true }
    ]
  }
}
```

**未就绪**（HTTP 503）

```json
{
  "code": 5000,
  "msg": "依赖不可用",
  "data": {
    "ready": false,
    "dependencies": [
      { "name": "postgres", "ok": false, "err": "connection refused" }
    ]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `name` | 依赖名称，如 `postgres` / `redis` |
| `ok` | 该依赖是否可用 |
| `err` | 不可用时的原因，可用时省略 |

> 各依赖**并发探测**，因此 `/readyz` 的总耗时约等于最慢的那一个，而非各依赖之和。

> 两个探针的分工：`/healthz` 用于判断「要不要重启容器」，
> `/readyz` 用于判断「要不要把流量摘掉」。混用会导致依赖抖动时容器被反复重启。

---

## 三、业务逻辑与链路解析

> 本章逐个说明接口的内部实现：经过哪些层、做了哪些校验、写了哪些表、
> 以及为什么这样设计。

### 3.1 账号功能

#### 注册 `POST /api/user/register`

**链路**

```
HTTP 请求
  └─▶ server/requireAuth（本接口不需要）
  └─▶ service.HTTPRegister
        ├─ 参数绑定与格式校验（gin binding）
        └─▶ biz.UserUseCase.Register
              ├─ email 转小写去空格 → 格式校验
              ├─ password 长度校验（6~32）
              ├─ gender 取值校验（0/1/2）
              ├─ repo.FindByEmail 查重 → 已存在返回 ErrEmailTaken
              ├─ bcrypt 哈希密码（DefaultCost=10）
              ├─ 昵称留空则随机生成 8 位
              └─▶ data.userRepo.Create（account_user 表，email 唯一约束）
```

**关键点**

- **查重靠数据库唯一约束兜底**：先 `FindByEmail` 是为了给出友好错误，
  但并发注册同一邮箱时会同时通过查询，最终由 `email` 唯一索引拦下，
  服务端把 SQLSTATE `23505` 翻译成 `4004`。
- **密码任何情况下都不明文落库**，出参也会清空 `Password` 字段。
- **昵称必填但允许留空**：留空时随机生成，避免客户端展示空昵称。

**涉及的表**：`account_user`

#### 登录 `POST /api/user/login`

**链路**

```
service.HTTPLogin
  └─▶ biz.UserUseCase.Login
        ├─ repo.FindByEmail → 不存在返回 401
        └─ bcrypt.CompareHashAndPassword 校验密码
  └─▶ auth.Sign(JWT_SECRET, uid, ttl)
        └─ 签发 HS256 令牌（claims: uid / jti / iat / exp）
  └─▶ biz.SessionUseCase.RegisterDevice
        ├─ ① tokenRepo.Save            → 写 account_token（权威：令牌是否有效）
        ├─ ② deviceStore.Add           → ZADD im:online:devices:<uid>
        └─ ③ evictExcess               → 超限则 ZRANGE 取最早的设备并吊销其令牌
```

**关键点**

- **邮箱不存在与密码错误返回同一个错误码 `4001`**，刻意不区分，
  否则攻击者可以据此枚举系统中存在哪些邮箱。
- 令牌**显式锁定 HS256**，防止算法混淆攻击（篡改为 `none` 或 `RS256`）。
- Account 与 Chat 使用**同一个 `JWT_SECRET`**，因此 Account 签发的令牌
  可以直接被 Chat 校验，无需额外的令牌交换。
- **令牌带 `jti` claim**：它是令牌的唯一标识，数据库只存 `jti` 而不存令牌原文。
  这样即使 `account_token` 表被读取，也无法据此伪造或复用令牌。

**为什么先落库、再登记设备、最后淘汰**（顺序是不变量）

1. **先写令牌表**：它是「令牌是否有效」的唯一权威。若先登记设备而落库失败，
   会出现「设备显示在线但令牌根本不存在」的假在线；
2. **再写有序集合**：记录上线时间，淘汰顺序完全依赖它；
3. **最后淘汰**：淘汰会产生新的吊销写操作，放在最后避免中途失败留下
   「已淘汰但未吊销」的不一致。

**降级行为**（都是刻意取舍，不是疏漏）

| 故障 | 行为 | 理由 |
| --- | --- | --- |
| 设备登记失败（Redis 不可用） | 登录仍成功，本次不参与上限计数 | 令牌已可用，不应因非关键组件抖动让用户登不进来 |
| 淘汰过程失败 | 登录仍成功，设备数可能暂时超限 | 由下次登录继续收敛 |

**涉及的表**：`account_user`、`account_token`

#### 登出 `POST /api/user/logout`

**链路**

```
service.HTTPLogout
  ├─ 取令牌：请求体 accessToken > Authorization 头
  └─▶ auth.ParseWithJTI → (uid, jti)
        └─ 解析失败 → 按「已登出」返回 success=true
  └─▶ biz.SessionUseCase.Logout
        ├─ tokenRepo.Revoke(jti, "logout")   → 置 revoked_at
        └─ deviceStore.Remove                → ZREM 释放在线位
```

**关键点**

- **令牌已经无效时仍返回成功**：客户端在令牌自然过期后调登出会拿到错误、
  被迫忽略它，反而让「登出」这件事变得不可靠。
- **必须同时释放设备在线位**：只吊销不释放的话，登出过的设备会一直占着
  上限名额，用户会发现自己「没登录任何地方却提示设备数超限」。

**涉及的表**：`account_token`；**涉及的 Redis 键**：`im:online:devices:<uid>`

#### 令牌校验模式（`TOKEN_MODE`）

令牌校验有两种模式，由配置切换。两者**共用同一份 JWT 解析代码**，
差别只在「是否回查存储」，因此不会出现两套签名校验逻辑各自演化、
其中一套出漏洞的情况。

| 模式 | 校验内容 | 优点 | 代价 |
| --- | --- | --- | --- |
| `self` | 仅 JWT 自身（签名 + `exp`） | 零存储依赖，校验极快，可脱离 DB/Redis 横向扩展 | 令牌在自然过期前**无法提前吊销** |
| `db` | JWT 自校验 + 数据库中该 `jti` 存在且 `revoked_at` 为空 | 支持即时吊销（踢下线、改密立即生效） | 每次校验多一次存储查询 |

**默认是 `db`**：多设备在线上限依赖「踢出即失效」。
若默认 `self`，被踢的设备在令牌自然过期前仍能继续使用，
用户会看到「明明被踢了却还能发消息」。

**返回值的约定**（这一区分很关键）

| 情况 | 返回 | 对外语义 |
| --- | --- | --- |
| 签名错 / 过期 / 缺 `uid` | `valid=false`，无 error | `401`，客户端重新登录 |
| db 模式：`jti` 缺失 / 记录不存在 / 已吊销 | `valid=false`，无 error | `401`，客户端重新登录 |
| db 模式：**存储查询故障** | `valid=false`，**有 error** | `5000`，服务端故障需告警 |

把「令牌被吊销」也当成 error 返回的话，调用方按 `err != nil` 处理会对外报
`5000`，把一次正常的重新登录变成服务故障。而存储故障时**必须拒绝放行**
（fail-closed）：db 模式的语义就是「必须确认未被吊销」，
放行等于让被踢掉的设备重新获得访问权。

#### 多设备在线上限

**数据结构**

```
Redis ZSET  key = im:online:devices:<uid>
            member = device_key   （d:<deviceId> 或 j:<jti>）
            score  = 上线时间（Unix 毫秒）
```

**为什么用有序集合**

| 需求 | 有序集合如何满足 |
| --- | --- |
| 按上线时间淘汰最早的设备 | 按 score 排序，`ZRANGE 0 n-1` 一次取出最早的一批 |
| 同一设备重复登录只占一个位置 | 以 member 去重，重新登录只更新 score，不新增成员 |

集合无序、列表无法按时间排序，两者都做不到这两点。

**淘汰流程**

```
新设备登录
  └─▶ ZADD（member=device_key, score=now）
  └─▶ ZRANGE 0 -1 取全部成员 → 数量 > MAX_DEVICES_PER_USER ?
        └─▶ ZRANGE 0 (超出数-1) 取最早的一批
              ├─ ZREM 移除在线标记
              ├─ tokenRepo.FindByDevice → 反查该设备的 jti
              └─ tokenRepo.Revoke(jti, "evicted") → 令牌立即失效
```

**每次重新读取规模而不是用「1 + 上次规模」推算**：并发登录（用户在多个端
同时点登录）时两个请求都会看到超限，按实际规模淘汰能让后到的那次多踢一台，
最终收敛到上限。

**`deviceId` 的作用**：客户端上报稳定的 `deviceId` 时用它作为 member，
同一台设备重新登录会覆盖同一个成员，不会自己把自己挤出上限。
未上报时回落到 `jti`（每次登录算一台新设备），这是更保守的行为——
宁可多踢，也不要让「不报 `deviceId` 的客户端」无限占用在线位。

**`ONLINE_DEVICE_TTL`**：有序集合本应由登出 / 被踢显式清理，
但客户端异常退出不会触发清理，长期运行会持续膨胀。
给一个较长的 TTL（默认 30 天）后长期不活跃的用户会被自然回收，
而活跃用户每次登录都会续写续期。

#### 本人资料 / 他人资料 `personal_info` · `query_user_info`

**链路**

```
service.HTTPPersonalInfo / HTTPQueryUserInfo
  ├─ currentUID 从 context 取 uid（由 requireAuth 中间件注入）
  └─▶ biz.UserUseCase.GetUser(id)
        └─▶ data.userRepo.FindByID
```

**关键点**

- 两个接口共用 `biz.GetUser`，只是取的 `userId` 不同：
  本人资料用令牌里的 uid，他人资料用请求参数。
- 这是 Chat 侧展示昵称头像的**数据来源**：Chat 不存用户资料，
  需要时经 gRPC `BatchGetUsers` 调用账号中心。

**涉及的表**：`account_user`

#### 改密码 `POST /api/user/reset_password`

**链路**

```
service.HTTPResetPassword
  └─▶ biz.UserUseCase.ResetPassword
        ├─ 新密码长度校验（6~32）
        ├─ repo.FindByEmail
        ├─ 校验旧密码（bcrypt 比对）
        └─▶ repo.UpdatePassword(新哈希)
  └─▶ biz.SessionUseCase.RevokeAll(uid, "password_reset")
        ├─ tokenRepo.RevokeAllByUser → 全部令牌置 revoked_at
        └─ deviceStore.Clear        → 清空设备在线标记
```

**关键点**

- 必须校验旧密码，因此该接口是**已登录但需二次验证**的语义。
- **改密后吊销全部旧令牌**：密码变更意味着「凭据已更换」，
  旧令牌若继续有效，任何已泄露的令牌都能绕过这次安全操作。
- **同时清空设备在线标记**：不清空的话，用户改密后重新登录会被旧设备的
  残留标记判为超限，表现为「刚改完密码，所有端都登不进去」。
- 吊销失败不回滚密码变更（密码已经改了，报错会让用户以为没改成功），
  但会记 Error 日志以便告警与人工介入。

**涉及的表**：`account_user`、`account_token`；**涉及的 Redis 键**：`im:online:devices:<uid>`

#### 改资料 `POST /api/user/modify_personal_info`

**链路**

```
service.HTTPModifyPersonalInfo
  └─▶ biz.UserUseCase.ModifyUserInfo(uid, nickname, gender, avatarURL)
        ├─ 昵称去空格，非空时校验长度 ≤ 32
        ├─ gender 仅在与现有值不同时更新
        ├─ avatarUrl 非空时更新
        └─▶ repo.Update
```

**关键点**：字段均可选，**只更新传入的项**，未传的保持原值。
这避免了「只想改头像却把昵称清空」这类问题。

---


### 3.2 好友功能

#### 发起好友申请 `POST /api/group/add_friend`

**链路**

```
service.HTTPAddFriend
  └─▶ biz.FriendUseCase.ApplyFriend(fromUID, toUID, applyMsg)
        ├─ 参数校验：双方 ID 合法、不能加自己、附言 ≤ 100
        ├─ repo.AreFriends → 已是好友则返回 (nil, alreadyFriends=true)
        ├─ repo.FindPendingRequest(from, to)
        │     ├─ 存在且未过期 → 直接复用，返回旧申请
        │     └─ 已过期 → 先置为「已过期」，让出待处理唯一键
        └─▶ repo.CreateRequest（friend_request 表）
```

**关键点**

- **「同一对用户只能有一条待处理申请」由数据库保证**：
  `friend_request` 上有一个**部分唯一索引**
  `UNIQUE (from_uid, to_uid) WHERE status = 0`。
  只约束 `status=0` 的行，因此历史申请（已同意/已拒绝）可以无限累积而不冲突。
- **申请有有效期（7 天）**：若不设过期，用户被拒绝后想再次申请就会被
  唯一约束挡死。过期时间一到，旧申请先被置为 `status=3`，把唯一键让出来。
- 重复点击申请不会产生多条记录，服务端直接复用待处理的那条。

**涉及的表**：`friend_request`

#### 处理好友申请 `POST /api/group/handle_friend`

**链路**

```
service.HTTPHandleFriend
  ├─ resolveFriendRequestID：优先用 requestId；
  │     旧契约只给 groupId 时，反查双方之间待处理的申请
  └─▶ biz.FriendUseCase.HandleFriend(operatorID, requestID, agree)
        ├─ repo.FindRequestByID → 不存在返回 4003
        ├─ 校验 req.ToUID == operatorID（只有收件人能处理）
        ├─ 校验 status == 待处理
        ├─ repo.UpdateRequestStatus(条件更新 WHERE status=0)
        │     └─ 返回 false 说明并发中已被别人处理 → 4004
        ├─ 拒绝：结束
        └─ 同意：
              ├─ repo.CreateRelation(双向两行，单事务)
              └─▶ convUC.EnsureSingleConversation（幂等建单聊会话）
```

**关键点**

- **条件更新是并发安全的核心**：`UPDATE ... WHERE id = ? AND status = 0`
  只会有一次真正生效。若是「先查再改」，两个并发请求都会通过检查，
  最终产生**两个单聊会话**。
- **会话创建失败不回滚好友关系**：好友已生效，会话可由客户端首次发消息时
  按需补建（`EnsureSingleConversation` 幂等）。反之回滚会让「已通过」的
  申请处于不一致状态。
- 旧契约的 `groupId` 解析依赖 `PeerOfSingle`：从单聊会话反查出对方，
  再查两人之间的待处理申请。

**涉及的表**：`friend_request`、`friend_relation`、`conversation`、`conversation_member`

#### 好友列表 `POST /api/friend/list`

**链路**

```
service.HTTPFriendList
  └─▶ biz.FriendUseCase.ListFriends(uid)
        ├─ repo.ListRelations(uid) → friend_relation 单条件索引扫描
        ├─ 收集 friendID 列表
        ├─▶ users.BatchGetBriefs(friendIDs)（gRPC 调账号中心批量取资料）
        └─ 组装 FriendDetail
```

**关键点**

- **好友关系双向存两行**，因此「我的好友」退化为 `user_id = ?` 的
  单条件索引扫描；若只存一行，查询条件会变成
  `user_id = ? OR friend_id = ?`，无法有效利用索引。
- **资料获取失败会降级**：返回备注名，昵称头像留空，而不是让整个列表失败。
  这是「主数据优先于修饰数据」的取舍。

**涉及的表**：`friend_relation`

#### 好友申请列表 `POST /api/friend/request_list`

**链路**

```
service.HTTPFriendRequestList
  ├─ status 指针判空：不传（nil）→ -1（不过滤）
  │     注意不能用 0 表示「未传」，因为 0 是「待处理」这一合法值
  └─▶ biz.FriendUseCase.ListFriendRequests(uid, status, limit)
        └─▶ repo.ListRequests（(to_uid, status, created_at DESC) 索引）
```

**关键点**：`status` 用**指针**接收，是为了区分「传了 0」与「没传」。
若用值类型，客户端传 `status=0` 会被误判为「不过滤」，返回全部状态的申请。

**涉及的表**：`friend_request`

#### 单聊会话的建立：`EnsureSingleConversation`

好友同意的核心动作，值得单独说明。

```
EnsureSingleConversation(uidA, uidB)
  ├─ bizKey = "S:" + min(uidA,uidB) + ":" + max(uidA,uidB)
  ├─ repo.FindByBizKey(单聊, bizKey)
  │     └─ 已存在 → 直接返回（幂等）
  └─ 不存在：
        ├─ 生成会话 ID（雪花）
        └─▶ repo.Create(会话 + 两条成员行，单事务)
```

**关键点**

- **单聊唯一性下沉到数据库**：`biz_key` 用 `LEAST/GREATEST` 消掉了
  「谁发起」这一维度，因此 A 找 B 与 B 找 A 得到**同一个键**，
  数据库的 `UNIQUE (type, biz_key)` 会拒绝重复会话。
- 若把唯一性交给应用层判断，并发下必然产生重复会话，
  且这类脏数据事后很难清理（消息已经挂在两个会话上）。

---

### 3.3 会话功能

#### 会话列表 `POST /api/group/message_group_info_list`

**链路**

```
service.HTTPMessageGroupInfoList
  └─▶ biz.ConversationUseCase.ListConversations(uid)
        ├─ repo.ListByUser(uid)
        │     └─ conversation JOIN conversation_member（一次联表，避免 N+1）
        │           WHERE m.user_id = ? AND m.left_at IS NULL
        │           ORDER BY c.updated_at DESC
        ├─ 单聊：repo.FindPeerIDs 批量取「对方」，再批量取昵称头像
        └─ 组装 UserConversation（含未读数、最后一条消息）
```

**关键点**

- **一次联表而不是按会话逐个查**：会话数上百时，N+1 会让响应时间明显劣化。
- **已退出的会话不出现**：条件带 `m.left_at IS NULL`。
- **未读数是独立维护的计数**，不是 `max_seq - last_read_seq`：
  后者在退群重进、消息撤回、消息删除等场景下都不准确。
- 排序用 `c.updated_at DESC`，而 `updated_at` 在每次发消息推进
  `max_seq` 时一并刷新，因此「最近活跃」无需额外计算。

**涉及的表**：`conversation`、`conversation_member`

#### 上报已读 `POST /api/group/mark_read`

**链路**

```
service.HTTPMarkRead
  └─▶ biz.ConversationUseCase.MarkRead(convID, uid, seq)
        └─▶ repo.UpdateMemberReadSeq
              UPDATE ... WHERE conversation_id=? AND user_id=? AND last_read_seq < ?
              SET last_read_seq = seq,
                  unread_count = GREATEST(unread_count - (seq - last_read_seq), 0)
```

**关键点**

- **位点只前进不回退**：条件里的 `last_read_seq < ?` 让「迟到的旧位点」
  影响 0 行，天然被忽略。若无条件覆盖，一个乱序上报就会把已读位点拉回去，
  已读消息重新变成未读。
- **未读数按推进量同比例扣减**而不是直接置零：客户端上报的位点可能落后于
  最新消息，此时仍有未读，置零会丢红点。
- `GREATEST(..., 0)` 兜底，避免并发下出现负数未读。

**涉及的表**：`conversation_member`

---


### 3.4 消息功能

消息是系统的核心，其链路最长、涉及组件最多，因此单独展开。

#### 发送消息 `POST /api/message/upload` 与 WS 上行发送

两个入口**共用同一套业务规则**，只是传输方式不同：

```
HTTP:  service.HTTPUpload        ─┐
                                  ├─▶ biz.MessageUseCase.Send
WS:    service.handleWSSend      ─┘
```

**完整链路**

```
① service 层：解析会话标识、取当前用户
        │
② biz.MessageUseCase.Send
        ├─ validateSendRequest
        │     ├─ 会话 ID / 发送者 ID 合法
        │     ├─ 消息类型 ∈ {1,2,3,4,5}
        │     ├─ 正文长度 ≤ 4096 字符（按 rune 计）
        │     └─ 幂等键长度 ≤ 64
        │
        ├─ convRepo.FindMember（非成员直接拒绝）
        │
        ├─ 幂等键缺失时由服务端生成 "srv-<base36 id>"
        │
        ├─ 幂等回查 repo.FindByConversationAndClientMsgID
        │     └─ 命中 → 直接返回既有消息，Duplicated=true
        │           （不再发号、不再落库、不再投递）
        │
        ├─ idGen.Next()             → 消息 ID（雪花）
        ├─ seqAllocator.Next()      → 会话内序号
        ├─ seqAllocator.NextSenderSeq() → 发送者维度序号
        │
        └─▶ data.messageRepo.Create（单事务，四步原子完成）
              ├─ INSERT message
              ├─ UPDATE conversation SET max_seq = seq  WHERE max_seq < seq
              ├─ UPDATE conversation_member SET unread_count = unread_count + 1
              │        WHERE user_id <> 发送者 AND left_at IS NULL
              └─ INSERT message_outbox（投递事件）
        │
③ relay.Run（后台协程，独立于请求）
        ├─ outboxRepo.FetchPending（FOR UPDATE SKIP LOCKED，捞取即置「投递中」）
        ├─ publisher.Publish（Kafka，key = 会话 ID）
        └─ outboxRepo.MarkDelivered（投递确认）
        │
④ consumer.Handle（订阅协程）
        ├─ 幂等去重（按 会话 + seq）
        ├─ loader.LoadMessage（按会话 + seq 精确查询）
        ├─ convRepo.ListMembers
        └─ registry.PushToUser（推给本节点上在线的成员）
        │
⑤ 客户端 B 的 WebSocket 收到 {"type":"message", ...}
```

**关键设计点**

**1. 执行顺序是刻意的**：先校验权限与参数（失败不消耗序号）→ 再查幂等
（重试不消耗序号，否则会留下空洞）→ 最后发号落库。

**2. 先发号再落库**，落库失败会浪费一个序号，表现为序号空洞。
这是可接受的：客户端按 seq 补洞时对**空洞**的容忍度远高于对**乱序**的容忍度，
而若改成先落库再发号，就必须引入两阶段提交。

**3. 幂等由数据库三重唯一约束强制**（不依赖客户端诚实）：

```sql
UNIQUE (conversation_id, seq)                        -- 顺序权威
UNIQUE (conversation_id, sender_id, client_msg_id)   -- 幂等权威
UNIQUE (conversation_id, sender_id, sender_seq)      -- 防止换 ID 重发绕过
```

第三条是关键：客户端只要换一个 `clientMsgId` 就能绕过第一重幂等，
但 `sender_seq` 的唯一性把「同一发送者的插入」也锁住了。

**4. 消息与事件在同一事务**：若分两次写，进程在「消息已写入、
事件未写入」之间崩溃时，这条消息**永远不会被推送**，而服务端却认为一切正常。
这是最难发现的一类消息丢失。

**5. 未读数在事务内累加**，且排除发送者自己，避免「有新消息但红点没亮」
或「自己发的消息给自己加未读」。

**6. 发送者也会收到推送**（由消费者统一推送）：多端登录时，
手机发了消息，网页端也要看到。网关**不再单独回执**，否则发送者会在
同一毫秒收到两条指向同一消息、但内容不同的推送。

**涉及的表**：`message`、`conversation`、`conversation_member`、`message_outbox`

#### Outbox 投递机制 `relay`

这是「消息不丢」的核心保障，值得展开。

**状态机**

```
   0 待投递 ──FetchPending（捞取即改状态）──▶ 3 投递中
      ▲                                          │
      │                                          ├─ 投递成功 ──▶ 1 已投递
      │                                          │
      ├──MarkFailed（退避重试，retry+1）──────────┤
      │                                          │
      ├──ReclaimStale（超租约回收）◀──────────────┤
      │                                          │
      └────────────── MarkDead ──▶ 2 死信 ◀──────┘（重试耗尽）
```

**为什么捞取时要改状态**

早期实现只读不改状态，于是每一轮轮询都会把同一批事件再捞一遍，
同一条消息被反复投递到队列、反复推送给用户——这正是「发送者收到两条推送」
的根源。修复的关键不是让消费者去重，而是**让生产者不重复投递**。

**为什么还需要「回收」机制**

「置为投递中」杜绝了重复，但代价是：若实例在投递途中被强杀，
该事件会永远停在「投递中」，再也无人问津——这是**消息丢失**。

因此必须有兜底：超过**租约**（1 分钟）仍未确认的，退回待投递重新投递。
租约取值需**远大于**单条投递超时（5 秒）：若接近甚至小于它，
正常的慢投递会被误判为滞留，从而被重复投递。

**为什么投递确认要带状态条件**

`MarkDelivered` 限定 `status = delivering`：只有真正被本轮捞取的事件
才能被确认。否则一个「迟到的确认」会把已被回收重投的事件又改成已投递，
造成漏投递——而漏投递比重复投递更难补救。

**死信终态**

重试次数耗尽后必须落地为 `status=2`（死信），而不是只打日志。
早期实现正是「日志说已转死信、数据库里却还在重试」，两者自相矛盾，
排查时会严重误导。

#### 消费与推送 `consumer`

**链路**

```
consumer.Handle(topic, key, value)
  ├─ json.Unmarshal 事件载荷（失败则丢弃并告警：格式不兼容重试也不会成功）
  ├─ 校验 conversationId / seq
  ├─ 幂等去重 —— 按 (会话, 序号) 的去重窗口
  ├─ loader.LoadMessage(convID, seq)
  ├─ convRepo.ListMembers(convID)
  └─ 遍历成员（按 500 分批）：
        └─ pusher.IsOnlineLocally(uid) ? PushToUser : 跳过
```

**关键设计点**

**1. 只推给本节点在线的成员**：其他节点上的成员由那些节点各自的消费者负责。
这正是「每实例独立消费者组」的由来——每个实例都要收到**全量**消息，
才能推给**自己节点上**的在线用户；若共用消费者组，消息会被实例瓜分，
表现为「部分用户永远收不到推送」。

**2. 消费侧幂等是第二道防线**：第一道在 Outbox（捞取即置投递中），
但仍有两条路径会产生重复——投递成功后确认失败、以及 Kafka 自身的
at-least-once 语义。因此消费端必须去重。

**3. 去重键必须包含会话维度**：每个会话都从 `seq=1` 开始，
若只用 seq 做键，第二个会话的第一条消息会被误判为重复而永远推不出去。

**4. 处理失败必须放行去重键**：否则一次瞬时故障就会让该消息永远无法重推，
把「去重」变成「丢消息」。

**5. 「用户不在线」不算错误**：那是最常见的正常情况，
若认定为失败会让监控指标失去意义。离线消息由客户端按 seq 拉取补齐。

#### 拉取消息 `POST /api/message/pull`

**链路**

```
service.HTTPPull
  ├─ 解析 conversationId（兼容 groupId）
  ├─ toSeq 兜底：兼容旧字段 maxMsgId
  └─▶ biz.MessageUseCase.Pull
        ├─ convRepo.FindMember（非成员拒绝）
        ├─ limit 归一化（默认 20，上限 200）
        ├─ convRepo.FindByID（取 maxSeq）
        └─▶ repo.ListBySeqRange
              WHERE conversation_id = ? AND seq > ? [AND seq <= ?]
              ORDER BY seq ASC|DESC LIMIT ?
```

**关键点**

- **`fromSeq` 是排他下界**（`seq > fromSeq`）：已读位点表达的是
  「这条我读过了」，因此下次拉取应从它之后开始，否则每次都重复返回已读的那条。
- **走 `(conversation_id, seq DESC)` 索引**，这是最热的查询路径。
- `hasMore` 用「是否取满 limit」判断，刻意不做 `count(*)` 避免多一次全表扫描。

#### 增量同步 `POST /api/message/sync`

链路与 `pull` 完全相同，只是固定 `ascending=true`。

**单独开路径的理由**：让「重连补洞」与「历史翻页」两种意图在调用侧就能区分，
便于后续对补洞路径单独做限流与监控——它们的流量特征与重要性完全不同。

#### 撤回消息 `POST /api/message/recall`

**链路**

```
service.HTTPRecall
  └─▶ biz.MessageUseCase.Recall(convID, messageID, operatorID)
        ├─ convRepo.FindMember（非成员拒绝）
        └─▶ repo.Recall（条件更新）
              UPDATE message
              SET status = 2, recalled_at = now(), recalled_by = ?
              WHERE id = ? AND conversation_id = ? AND sender_id = ? AND status = 1
              └─ 影响 0 行 → ErrMessageNotRecallable
```

**关键点**

- **三个约束放进同一条 WHERE**：属于该会话、由本人发送、当前处于正常状态。
  若是「先查再改」，并发下两次撤回都会成功。
- 影响 0 行时统一返回 `4004`，不区分「不存在」「非本人」「已撤回」——
  这些细节对客户端没有意义，反而会泄露消息是否存在。
- **不内置时间限制**：撤回时限属于产品策略（如微信的 2 分钟），
  写死在领域层会让不同业务线无法差异化配置。需要时应由调用方自行判断。

**涉及的表**：`message`

---


### 3.5 群聊功能

#### 创建群聊 `POST /api/group/create_group_chat`

**链路**

```
service.HTTPCreateGroupChat
  └─▶ biz.GroupUseCase.CreateGroup(ownerID, name, memberIDs)
        ├─ 校验 ownerID 合法
        ├─ 群名去空格，非空且长度 ≤ 50
        ├─ idGen.Next() → 会话 ID（先取 ID 才能拼 biz_key）
        ├─ 成员去重 + 滤除自己，创建者插入为「群主」
        ├─ 校验总人数 ≤ 500
        └─▶ convRepo.Create(会话 + 全部成员行，单事务)
```

**关键点**

- **先取 ID 再拼 `biz_key`**：群聊没有天然的唯一键，直接用自己的 ID
  （`G:<会话ID>`），这样一次插入即可完成，避免「先占位再回写」在并发下撞唯一约束。
- **先确定成员再去重，最后写 `member_count`**：这样计数在插入时一次写对，
  避免「先建会话、再回写人数」产生不一致。
- **创建者角色是 `MemberRoleOwner`（2）**，其余成员为 `MemberRoleMember`（0）。
- **不校验被邀请人是否为好友**：拉人入群时逐个校验好友会引入 N 次跨服务调用，
  且许多产品形态本就不要求好友关系。若要限制，应在网关或上游业务层加白名单。

**涉及的表**：`conversation`、`conversation_member`

#### 拉人入群 `POST /api/group/add_group_chat`

**链路**

```
service.HTTPAddGroupChat
  └─▶ biz.GroupUseCase.AddMembers(convID, operatorID, userIDs)
        ├─ mustBeMember(operatorID)          → 非成员 403
        ├─ repo.ListMembers 取现有成员，校验总数 ≤ 500
        └─▶ convRepo.AddMembers（ON CONFLICT DO UPDATE SET left_at = NULL）
              └─ RowsAffected > 0 时 UPDATE member_count = member_count + n
```

**关键点**

- **权限校验在 biz 层**：`mustBeMember` 拦住非成员，不依赖前端隐藏按钮。
  但**当前只校验身份，不校验角色**——任何群成员都能拉人入群。
  若产品要求「仅群主/管理员可拉人」，需要在 biz 层补角色判断。
- **`ON CONFLICT DO UPDATE SET left_at = NULL`** 而不是单纯的 `DO NOTHING`：
  退过群的人再次入群时，若只做 `DO NOTHING`，会因唯一约束而加不进来，
  表现为「拉人成功但对方看不到群」这类难查的问题。
- **`RowsAffected` 精确反映真实新增人数**，因此 `cnt` 不会把「已在群内」
  的人重复计入。
- **`member_count` 用表达式自增**而不是「先读再写」：并发拉人时后者会丢更新。
  计数只增不减（成员是软删除，行仍在）。

**涉及的表**：`conversation`、`conversation_member`

#### 群成员 ID 列表 `POST /api/group/group_user_list`

**链路**

```
service.HTTPGroupUserList
  └─▶ biz.GroupUseCase.ListMemberIDs(convID, uid)
        ├─ mustBeMember(uid)   → 非成员 403
        └─▶ convRepo.ListMembers → 提取 UserID
```

**关键点**：**成员校验不可省**。若只按 `conversationId` 返回成员列表，
任何登录用户只要猜到会话 ID 就能拿到群成员名单，这是信息泄露。

#### 群成员详情 `POST /api/group/member_list`

**链路**

```
service.HTTPGroupMemberList
  ├─ withProfile 默认 true
  └─▶ biz.GroupUseCase.ListMembers(convID, uid, withProfile)
        ├─ mustBeMember(uid) → 非成员 403
        ├─ convRepo.ListMembers（ORDER BY role DESC, joined_at ASC）
        └─ withProfile 时：users.BatchGetBriefs（gRPC 批量取昵称头像）
```

**关键点**

- **排序稳定**：先按角色倒序（群主、管理员在前），再按加入时间正序。
  若只按角色排序，同角色成员的顺序不确定，客户端展示会跳动。
- **`withProfile=false` 可跳过资料查询**：只需展示成员数量或做批量判断时，
  省掉一次跨服务调用。

**涉及的表**：`conversation_member`

#### 退出群聊 `POST /api/group/quit`

**链路**

```
service.HTTPQuitGroup
  └─▶ biz.GroupUseCase.QuitGroup(convID, uid)
        ├─ mustBeMember(uid)              → 非成员 403
        ├─ 校验 role != 群主              → 群主退出返回 403
        └─▶ convRepo.RemoveMember（软删除）
              UPDATE conversation_member
              SET left_at = now(), unread_count = 0
              WHERE conversation_id = ? AND user_id = ? AND left_at IS NULL
```

**关键点**

- **群主不能直接退出**：群聊会失去所有者。正确做法是先转让群主或解散群聊。
- **软删除而非物理删除**，三个理由：保留「谁什么时候退的群」便于追溯与合规；
  重新入群可复用同一行，不会因唯一约束冲突而失败；历史消息的发送者引用
  不会变成悬空。
- **重复调用天然幂等**：条件里限定 `left_at IS NULL`，已离开的行不会被再次更新。

**涉及的表**：`conversation_member`

---

### 3.6 WebSocket 实时通信

#### 连接建立与生命周期

**链路**

```
GET /ws?token=xxx
  ├─ authenticate：从 Query 或 Authorization 头取令牌，校验 JWT
  │     └─ 失败 → 直接 401，不建立连接
  ├─ upgrader.Upgrade → 升级为 WebSocket
  ├─ newConn（生成 ConnID、平台标识）
  ├─ registry.Add(c)               → 加入本节点连接表
  ├─ presence.Online(uid)          → 写入跨节点路由（Redis Hash）
  └─ 启动两个协程，各司其职：
        ├─ writePump：唯一写协程，消费 send 队列 + 定期发 ping
        └─ readPump ：读协程，阻塞等客户端数据，处理上行
```

**为什么读写分两个协程**：读协程阻塞等客户端数据，写协程阻塞等下行队列。
合并成一个协程会导致任一方阻塞时另一方也停摆。

**为什么写必须集中在一个协程**：`gorilla/websocket` **不允许并发写**，
集中到一个协程是唯一安全的做法。

**连接退出时的清理**（`defer` 保证任何异常路径都不遗漏）

```
c.Close()               → 通知写协程退出
registry.Remove(c)      → 从本地连接表摘除
presence.Offline(uid)   → 清除跨节点路由
conn.Close()            → 关闭底层 TCP
```

#### 心跳机制

```
服务端 writePump 每 50s ──▶ 发 WebSocket Ping 帧
客户端            ──▶ 自动回 Pong 帧
服务端 readPump   ──▶ SetPongHandler：刷新活跃时间 + 延后读超时
若 60s 未收到 Pong ──▶ 读超时触发，连接被清理
```

**为什么靠 Pong 判断存活**：TCP 层在客户端异常断电等场景下**不会主动通知**服务端，
连接会一直停留在「已建立」状态。心跳是判断连接是否已死的唯一可靠依据。

#### 上行处理

```
readPump 收到文本帧
  ├─ SetReadLimit(4096) 限制单条大小
  ├─ json.Unmarshal → ws.UpstreamMessage
  │     └─ 自定义 UnmarshalJSON：conversationId 兼容数字与字符串
  ├─ type == "ping"  → 网关层直接回 pong，不进业务层
  └─ 其他 type → handler(ctx, uid, msg) → service.HandleUpstream
        ├─ type == "send" → handleWSSend → biz.MessageUseCase.Send
        └─ 未知 type      → 记日志后忽略（不报错，利于灰度升级）
```

**关键点**

- **心跳在网关层应答**：心跳是传输层关注点，让它穿过业务层只会增加无谓开销。
- **`conversationId` 必须兼容字符串**：19 位雪花 ID 超出 JS 安全整数范围，
  客户端用 `JSON.parse` 读回来会被静默舍入成另一个值，再回传就指向
  不存在的会话。此前只接受数字会导致这类请求被判为「消息格式不正确」，
  而该报错完全不指向真实原因。

#### 下行推送与跨节点路由

```
consumer 决定推送
  └─▶ registry.PushToUser(uid, msg)
        ├─ json.Marshal（集中序列化，避免各调用点编码不一致）
        └─▶ Deliver(uid, payload)
              ├─ LocalConns(uid) → 本节点该用户的所有连接
              └─ 逐条 trySend：
                    ├─ 成功 → 进入该连接的 send 队列，由 writePump 发出
                    └─ 队列满/已关闭 → 主动断开该连接
```

**多端同时在线的支持**

`Registry` 用 `userId -> map[connID]*Conn` 的**一对多**映射，
而不是一对一。若只保留最后一条连接，先登录的端会静默收不到消息，
表现为「手机上登录后，网页端再也收不到消息」——这类问题在单端测试中
完全测不出来。

**队列满时主动断开而非阻塞**

投递方是消息消费者，**绝不能因为某条慢连接而卡住**。缓冲满说明该连接
确实消费不过来，主动断开比无限积压更合理；客户端重连后可通过 seq 补洞
拿回缺失消息。

**跨节点路由（Presence）**

```
Redis Hash: im:route:{userId}
  field = nodeId
  value = 该节点最后一次心跳的 Unix 毫秒时间戳
  TTL   = 180s
```

为什么用「节点 + 心跳时间戳」而不是「节点列表」：多节点部署时同一用户
可能被不同节点持有（重连漂移），记录时间戳可以判断哪个节点仍然存活。
若只存节点列表，节点异常退出后残留的脏数据会导致消息投递给已不存在的节点，
表现为「消息发出但对方收不到」。超时节点在查询时被**惰性清理**，
不需要独立的后台清理任务。

#### 优雅退出

```
kratos.BeforeStop
  ├─ cancelBg()                → 取消后台协程（relay 停止投递）
  └─ wsServer.Stop()           → registry.CloseAll() 关闭全部连接
        └─ 对每条连接发 WebSocket Close 帧，客户端立即感知并触发重连
```

**刻意不等待客户端确认**：退出流程有时间上限，等待会拖长停机时间。
客户端感知到断开后会自行重连，并通过 seq 补齐消息。

---

### 3.7 配置装载与分级校验

虽然不是接口，但它决定了服务能否启动，与所有接口相关。

**装载流程**

```
conf.Load()
  ├─ loadDotEnv(".env")   → 真实环境变量优先，不会被文件覆盖
  ├─ 逐项读取环境变量
  └─ Check() → 按严重级别分类
        ├─ 致命（LevelFatal）→ 汇总全部问题后返回 error，服务拒绝启动
        └─ 可降级（LevelWarn）→ 暂存，由 main 在日志就绪后告警
```

**为什么分级**

| 级别 | 判定依据 | 字段 |
| --- | --- | --- |
| 致命 | 缺了它服务无法提供任何有意义的服务 | `Environment`、`ServiceName`、DB 连接信息、`HTTP_ADDR`、`JWT_SECRET`、`ACCOUNT_RPC_ENDPOINT`、`TOKEN_MODE` 取值、`MAX_DEVICES_PER_USER` 非负、生产环境的密钥强度与数据库密码 |
| 可降级 | 服务能启动并接受请求，只是能力受限 | `CACHE_ADDRS`（序号分配与在线路由不可用；Account 侧多设备上限退化为不限制）、非生产环境使用 `log` 投递 |

把所有依赖都设为硬依赖，会让任何一个非关键组件抖动都演变成整个服务不可用，
反而降低可用性。

**Account 侧的降级项**

| 配置问题 | 降级表现 |
| --- | --- |
| `CACHE_ADDRS` 为空 | 多设备在线上限退化为不限制；被踢设备无法即时失效（`db` 模式仍可用，只是校验全部落到数据库） |
| `TOKEN_MODE=db` 且缓存为空 | 令牌校验直接查数据库，延迟显著上升（因此单独提示一条告警） |

**为什么 `TOKEN_MODE` 取值错误是致命的**

取值写错会让服务在「以为开启了严格校验」的假设下退化为 `self` 模式，
被踢下线的设备仍能继续使用——这是**静默的安全降级**，
比启动失败危险得多，必须在启动时拦住。

**为什么关键字段不给默认值**

`DB_HOST` / `DB_NAME` / `ACCOUNT_RPC_ENDPOINT` / `CACHE_ADDRS` 都**刻意不设默认值**。
给了兜底值会把「配置缺失」悄悄变成「用默认值连接」，例如连到另一个数据库、
或让降级告警永远不触发。缺省时必须明确报错。
`DB_PORT`（5432）、`DB_USER`（postgres）保留了默认值——它们有行业通用值，
给了不会掩盖配置错误。

**运行时可调的业务参数**（Account）

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKEN_MODE` | `db` | `self`（仅校验 JWT）/ `db`（还需数据库未吊销） |
| `MAX_DEVICES_PER_USER` | `3` | 单账号同时在线设备数上限，`0` 表示不限制 |
| `ONLINE_DEVICE_TTL` | `720h`（30 天） | 设备在线标记（Redis ZSET）的兜底有效期，`0` 表示不过期 |

---

## 四、附录

### 4.1 完整接口清单（速查）

| # | 服务 | 方法 | 路径 | 功能分类 |
| --- | --- | --- | --- | --- |
| 1 | Account | POST | `/api/user/register` | 账号 |
| 2 | Account | POST | `/api/user/login` | 账号 |
| 3 | Account | POST | `/api/user/logout` | 账号 |
| 4 | Account | POST | `/api/user/personal_info` | 账号 |
| 5 | Account | POST | `/api/user/query_user_info` | 账号 |
| 6 | Account | POST | `/api/user/reset_password` | 账号 |
| 7 | Account | POST | `/api/user/modify_personal_info` | 账号 |
| 8 | Chat | POST | `/api/group/add_friend` | 好友 |
| 9 | Chat | POST | `/api/group/handle_friend` | 好友 |
| 10 | Chat | POST | `/api/friend/list` | 好友 |
| 11 | Chat | POST | `/api/friend/request_list` | 好友 |
| 12 | Chat | POST | `/api/group/message_group_info_list` | 会话 |
| 13 | Chat | POST | `/api/group/mark_read` | 会话 |
| 14 | Chat | POST | `/api/message/upload` | 消息 |
| 15 | Chat | POST | `/api/message/pull` | 消息 |
| 16 | Chat | POST | `/api/message/sync` | 消息 |
| 17 | Chat | POST | `/api/message/recall` | 消息 |
| 18 | Chat | POST | `/api/group/create_group_chat` | 群聊 |
| 19 | Chat | POST | `/api/group/add_group_chat` | 群聊 |
| 20 | Chat | POST | `/api/group/group_user_list` | 群聊 |
| 21 | Chat | POST | `/api/group/member_list` | 群聊 |
| 22 | Chat | POST | `/api/group/quit` | 群聊 |
| 23 | Chat | GET | `/ws` | 实时通信 |
| 24 | 两者 | GET | `/healthz` | 运维 |
| 25 | 两者 | GET | `/readyz` | 运维 |

### 4.2 约束速查

| 项 | 值 | 位置 |
| --- | --- | --- |
| 邮箱格式 | 必须合法 | Account 注册 |
| 密码长度 | 6 ~ 32 | Account 注册 / 改密 |
| 昵称长度 | ≤ 32 字符 | Account 改资料 |
| 单账号在线设备数 | 默认 3，`0` 表示不限制 | Account 登录（`MAX_DEVICES_PER_USER`） |
| 令牌校验模式 | 默认 `db`（需数据库未吊销） | Account 校验（`TOKEN_MODE`） |
| 设备在线标记 TTL | 默认 720h | Account 登录（`ONLINE_DEVICE_TTL`） |
| 好友申请附言 | ≤ 100 字符 | Chat 加好友 |
| 好友申请有效期 | 7 天 | Chat 加好友 |
| 消息正文长度 | ≤ 4096 字符（按 rune） | Chat 发送消息 |
| 幂等键长度 | ≤ 64 字符 | Chat 发送消息 |
| 消息类型 | `1` 文本 / `2` 图片 / `3` 视频 / `4` 音频 / `5` 系统（**无默认值，必传**） | Chat 发送消息 |
| 拉取条数 | 默认 20，最大 200 | Chat 拉取 / 同步 |
| 好友申请列表条数 | 默认 50，最大 200 | Chat 申请列表 |
| 群名长度 | 1 ~ 50 字符 | Chat 创建群聊 |
| 群成员上限 | 500（含创建者，故初始成员最多 499） | Chat 建群 / 拉人 |
| 群成员角色 | `0` 成员 / `1` 管理员 / `2` 群主 | Chat 成员详情 |
| WS 上行单条上限 | 4096 字节 | Chat WebSocket |
| WS 发送缓冲 | 256 条 | Chat WebSocket |
| WS ping 间隔 / pong 超时 | 50s / 60s | Chat WebSocket |
| Outbox 投递租约 | 1 分钟 | Chat relay |
| Outbox 最大重试 | 10 次 | Chat relay |

### 4.3 客户端接入建议

**首次接入的顺序**

1. 调 `register` 创建账号 → 调 `login` 拿到 `accessToken` 与 `userId`；
2. 用 `accessToken` 建立 WebSocket 长连接；
3. 调 `message_group_info_list` 拉会话列表，作为消息页首屏；
4. 对 `maxSeq > lastReadSeq` 的会话调 `sync` 补齐离线消息；
5. 之后依赖 WebSocket 推送实时更新。

**多设备接入的必做事项**

1. **首次启动生成并本地持久化 `deviceId`**（UUID 即可），每次登录都带上。
   不持久化的话，每次登录都会被当成一台新设备，用户会发现
   「只登录了两次就提示设备数超限」。
2. **处理 `evictedDevices`**：登录响应里出现该字段说明有其他设备被踢下线。
   可以提示用户「你已在其他设备退出登录」。
3. **退出登录时调 `/api/user/logout`**，并带上登录时相同的 `deviceId`；
   否则该设备会一直占着在线位，直到令牌自然过期。
4. **收到 `code=4001` 时不要自动重试**：可能是被其他端挤下线了，
   应引导用户重新登录，而不是循环重连。

**发送消息的推荐做法**

1. 为每条消息生成稳定的 `clientMsgId`（如 UUID）；
2. **必须显式传 `type`（HTTP）或 `msgType`（WS）**，取值 `1`~`5`，没有默认值；
3. 优先走 WebSocket `send`（延迟更低），网络异常时回退到 HTTP `upload`；
4. 超时重试时**复用同一个 `clientMsgId`**，服务端会返回原消息而不是新建。

**必须注意的五点**

1. **ID 当字符串处理**：19 位雪花 ID 超出 JS 安全整数范围，
   `JSON.parse` 会静默舍入。服务端已全部下发为字符串，客户端不要转成 `Number`。
2. **判断响应体的 `code` 而非 HTTP 状态码**：业务接口成功与失败都返回 HTTP 200。
3. **`fromSeq` 是排他下界**：已读到 `seq=5` 就传 `fromSeq=5`，
   否则会重复返回已读的那条。
4. **用 seq 判断消息缺失**：收到 `seq=10` 但本地最后一条是 `seq=7`，
   说明漏了 8、9，应调 `sync(fromSeq=7)` 补洞——不要依赖推送的完整性。
5. **`deviceId` 必须持久化**：见上文「多设备接入的必做事项」。

**重连后的标准流程**

```
1. 重新建立 WebSocket（携带有效的 accessToken）
2. message_group_info_list 取各会话的 maxSeq 与 lastReadSeq
3. 对落后的会话逐个 sync(fromSeq=lastReadSeq)
     hasMore 为 true 时，以返回的 maxSeq 作为新的 fromSeq 继续拉
4. 补齐后恢复正常实时接收
```

### 4.4 错误排查指引

| 现象 | 可能原因 | 排查方向 |
| --- | --- | --- |
| 接口返回 200 但数据为空 | 客户端回传的 ID 被 JS 舍入成了另一个值 | 检查是否把 ID 当字符串处理 |
| 业务报错但 HTTP 状态码是 200 | 这是设计如此：业务结果在响应体的 `code` 里 | 判断 `code` 而非 HTTP 状态码 |
| 收到 `code=4001` | 令牌过期，或 Account 与 Chat 的 `JWT_SECRET` 不一致 | 对比两服务的 `JWT_SECRET` |
| 登录后被立刻踢下线 | 是本人其他端登录触发了设备上限 | 看登录响应体的 `evictedDevices`；调大 `MAX_DEVICES_PER_USER` 或让客户端上报稳定的 `deviceId` |
| 明明只有一台设备却提示超限 | 客户端每次登录都换了新的 `deviceId`（或未上报），每次登录都算一台新设备 | 让客户端持久化 `deviceId`；查 `im:online:devices:<uid>` 的成员数 |
| 被踢的设备仍能继续发消息 | `TOKEN_MODE=self`，该模式按设计无法提前吊销令牌 | 改为 `TOKEN_MODE=db` |
| 改密后其他端没掉线 | 吊销全部令牌失败（存储故障） | 搜日志关键词「改密后吊销全部令牌失败」 |
| 校验令牌报 `5000` | `db` 模式下数据库查询故障（fail-closed 拒绝放行） | 检查 `DB_HOST` 连通性与 `/readyz` |
| 发送消息报 `5000` | Redis 不可用（序号分配依赖它） | 检查 `CACHE_ADDRS` 与 Redis 连通性 |
| 消息已发送但对方收不到 | 对方不在任何网关节点上（离线），或推送链路断开 | 查 `message_outbox` 表的状态分布 |
| 同一条消息推送多次 | Outbox 事件被重复投递 | 查 `message_outbox` 是否有重复 `partition_key` |
| 群成员列表返回 `4006` | 不是该会话成员 | 确认调用方是否在会话中 |
| 拉人入群返回 `4006` | 操作者不是群主或管理员 | 权限已收紧为「群主 / 管理员」，普通成员不能拉人 |
| 拉人入群返回 `4004` | 群成员数将超出 500 上限 | 与 `ErrInvalidParam` 区分开，客户端可据此换策略（如新建群） |
| 服务启动即退出 | 配置存在致命问题 | 看启动输出，问题会一次列全 |

**排查用的关键 SQL**

```sql
-- Outbox 状态分布：正常应几乎全是 status=1，且 stuck_delivering 为 0
SELECT status, count(*) FROM message_outbox GROUP BY status;
SELECT count(*) AS stuck FROM message_outbox WHERE status = 3;

-- 检查某会话的序号是否连续（有空洞说明发号后落库失败）
SELECT seq, sender_id, content FROM message
WHERE conversation_id = <会话ID> ORDER BY seq;

-- 检查是否产生了重复的单聊会话（应为 0 行）
SELECT biz_key, count(*) FROM conversation
WHERE type = 1 GROUP BY biz_key HAVING count(*) > 1;

-- 某用户当前的令牌状态：被踢的设备 revoked_by = 'evicted'
SELECT jti, device_id, platform, expire_at, revoked_at, revoked_by
FROM account_token WHERE user_id = <用户ID> ORDER BY created_at DESC;

-- 每个用户的在线令牌数：应不超过 MAX_DEVICES_PER_USER
SELECT user_id, count(*) FROM account_token
WHERE revoked_at IS NULL AND expire_at > now()
GROUP BY user_id HAVING count(*) > 3;
```

**排查用的关键 Redis 命令**

```
# 某用户当前在线的设备及其上线时间（score 为 Unix 毫秒）
ZRANGE im:online:devices:<用户ID> 0 -1 WITHSCORES

# 有序集合的成员数（应与该用户未吊销的令牌数一致）
ZCARD im:online:devices:<用户ID>
```

### 4.5 相关文档

| 文档 | 内容 |
| --- | --- |
| [README.md](file:///d:/GoCode/IMmessage/im/README.md) | 技术栈、架构分层、数据模型、配置说明、构建部署、容量评估 |
| [REFACTOR_PLAN.md](file:///d:/GoCode/IMmessage/im/REFACTOR_PLAN.md) | 重构章程、各阶段验收结果、缺陷修复记录 |
| [.env.example](file:///d:/GoCode/IMmessage/im/.env.example) | 全量环境变量模板 |
| [0001_init.up.sql](file:///d:/GoCode/IMmessage/im/Chat/migrations/0001_init.up.sql) | Chat 库建表脚本（含全部约束与注释） |
| [0002_token.up.sql](file:///d:/GoCode/IMmessage/im/Account/migrations/0002_token.up.sql) | Account 库在线令牌表建表脚本 |

---

## 附：文档维护约定

- 本文档是**接口的唯一索引入口**，新增或修改接口时必须同步更新：
  1. 第一节的分类索引表；
  2. 第二节的接口详情；
  3. 第三节的业务链路解析。
- 修改 `internal/service/http_*.go` 中的 DTO 字段时，
  需同步更新对应的参数表与响应示例。
- 修改 `internal/biz/` 中的约束常量（如长度上限）时，
  需同步更新 [4.2 约束速查](#42-约束速查)。
- 修改 `internal/conf/` 中的环境变量（新增、改默认值、改校验级别）时，
  需同步更新 [3.7 配置装载与分级校验](#37-配置装载与分级校验) 与
  [.env.example](file:///d:/GoCode/IMmessage/im/.env.example)。
- 架构层面的变更请改 [README.md](file:///d:/GoCode/IMmessage/im/README.md)，
  本文档只关注接口与链路。

