# 群运营 Webhook CLI 使用说明书（v0.1.2）

此功能向已启用的 Webhook 群运营计划提交一次动态发送任务。每次请求指定话术和已绑定群的子集，无需标准编排节点。当前企微发送方式是企业群发任务，运营成员确认后才会发送；指定群需企微终端 4.1.10 或以上。

## 1. 安装并选择环境

下载 [v0.1.2](https://github.com/qianlan33333-png/aicrm-cli/releases/tag/v0.1.2) 中对应系统的包；对照 `checksums.txt` 校验 SHA-256，解压并将可执行文件放入 PATH。源码安装需要 Go 1.26.6 或以上：

```sh
go install github.com/qianlan33333-png/aicrm-cli/cmd/aicrm-cli@v0.1.2
aicrm-cli --version
aicrm-cli profile add crm https://www.youcangogogo.com
aicrm-cli profile use crm
aicrm-cli profile list
```

已有同名 profile 时直接使用。测试环境建立另一个 profile；密钥精确绑定 HTTPS Origin，每个环境单独配置。

## 2. 配置 Webhook 签名密钥

这是部署方配置的 `AICRM_GROUP_OPS_WEBHOOK_SECRET`，不是账号密码、OAuth 令牌或 Webhook 地址最后一段。由部署负责人或受保护密钥系统提供；不要写在命令参数、请求 JSON、脚本源码、日志、聊天或 GitHub 中。

```sh
# 交互时隐藏输入，保存到独立的系统密钥环槽位。
aicrm-cli group-ops webhook configure
aicrm-cli group-ops webhook status

# 自动化可通过受保护 stdin 配置。
# aicrm-cli group-ops webhook configure --secret-stdin < /PROTECTED/PATH/secret
```

`status` 仅检查本机配置，不请求服务器，不证明密钥与服务器相同或 Provider 可用。Webhook 密钥与人类/机器登录分别保存，不覆盖已有账号会话；密钥环不可用时拒绝配置，不落明文。

自动化也可由密钥管理系统注入两个环境变量：

| 变量 | 含义 |
|---|---|
| `AICRM_CLI_GROUP_OPS_WEBHOOK_SECRET` | 当前环境的签名密钥 |
| `AICRM_CLI_GROUP_OPS_WEBHOOK_ORIGIN` | 与 profile 完全相同的 HTTPS Origin |

环境注入优先于密钥环。Origin 缺失或不匹配时拒绝使用；不要在共享 shell 历史中直接输入密钥。密钥长度沿服务端合同为 32–4096 字节，不允许首尾空白。

## 3. 读取计划及目标群引用

发现计划需要现有管理员登录；发送 Webhook 本身只使用签名密钥，不转发人类 Cookie 或机器令牌。

```sh
aicrm-cli auth login --username YOUR_EXISTING_USERNAME
aicrm-cli auth whoami
aicrm-cli doctor
aicrm-cli group-ops group-ops-plan get --param plan_id=12
aicrm-cli group-ops group-ops-webhook-descriptor get --param plan_id=12
aicrm-cli group-ops group-ops-plan-groups list --param plan_id=12 --query limit=100
```

将描述符的 `data.reference` 填入请求的 `webhook_reference`。目标取绑定群响应的 `data.items[].asset_reference`；按展示名称人工选定后复制准确引用，不传群名称或临时猜测群 ID。可以指定一个或多个绑定群，不能扩展到计划外的群。

计划应为 `webhook` 类型、已启用且已配置 Webhook，绑定有效运营人员及目标群。修改运行中计划的绑定前先暂停，编辑后再显式启用。

## 4. 准备请求 JSON

保存为 `groupops-request.json`，将占位符换成上一步读取的真实引用：

```json
{
  "webhook_reference": "REPLACE_WITH_WEBHOOK_REFERENCE",
  "target_chat_references": ["REPLACE_WITH_BOUND_ASSET_REFERENCE"],
  "messages": [
    {"type": "text", "text": "今晚 8 点直播，欢迎参加。"}
  ]
}
```

文字最多一个且只能位于数组首项，不允许 NUL 或首尾空白，可保留文本内换行和 Tab。文字最多 1000 个字符。文字可省略，但 `messages` 不能为空。

图片和文件先通过原素材库上传，取得本地数字 ID，然后使用如下消息数组：

```sh
aicrm-cli materials legacy-media-image upload \
  --upload image=cover.png --idempotency-key groupops-image-20261014-001
aicrm-cli materials legacy-media-attachment upload \
  --upload attachment=guide.pdf --idempotency-key groupops-file-20261014-001
```

图片上传响应的 `data.item_id`（或 `data.item.id`）作为 `image_id`；附件响应的 `data.id`（或 `data.item.id`）作为 `attachment_id`。大附件继续使用原分片上传/complete 合同，取 `attachment_id`。上传属于真实素材写操作，使用原管理员登录权限。

```json
[
  {"type": "text", "text": "资料已整理好，请查收。"},
  {"type": "image", "image_id": 123},
  {"type": "file", "attachment_id": 456}
]
```

`image_id` 和 `attachment_id` 必须是正整数 JSON 数字，不能是字符串。最多九个附件，保持数组顺序。Webhook 不接收原始图片/文件、任意 URL 或 Provider `media_id`；上传仍使用管理员 Cookie/CSRF 的原素材接口。素材可用性由服务端检查。

小程序优先引用已启用的本地小程序素材：

```json
{"type": "miniprogram", "miniprogram_id": 789}
```

`miniprogram_id` 与 `appid`、`path`、`title` 互斥，最多一个小程序。未准备素材时，当前已支持的日课自动封面请求形态为：

本地小程序素材可通过 CLI 创建，先准备 `miniprogram.json`：

```json
{
  "name": "今日内容卡片",
  "appid": "YOUR_APPROVED_APPID",
  "pagepath": "pages/content/index?id=YOUR_CONTENT_ID",
  "title": "内容详情",
  "thumb_image_id": 123,
  "enabled": true
}
```

```sh
aicrm-cli materials media-mini-program create \
  --input miniprogram.json --idempotency-key groupops-mini-20261014-001
```

取响应 `data.item.id` 作为 `miniprogram_id`。创建素材使用原管理员权限，不发送群消息。日课自动封面形态如下：

```json
{
  "type": "miniprogram",
  "appid": "wx0ca836834b18e989",
  "path": "pages/article/article?lesson_id=REPLACE_WITH_LESSON_UUID&from=learn",
  "title": "今日课程"
}
```

此形态仅支持已实现的 AppID 与严格日课 path 规则；使用真实小写 UUID，不增加额外查询参数。其他页面先上传封面并创建本地小程序素材，使用 `miniprogram_id`。标题最多 64 个 UTF-8 字节，不能含控制字符或首尾空白。

请求必须为 UTF-8 JSON、最大 64 KiB；重复 JSON 成员、重复目标、未知字段及不支持的消息形状会被本地拒绝。

## 5. 预检查与提交

为这次逻辑发送确定一个稳定事件 ID，例如 `groupops-20261014-live-001`。事件 ID 必须是 16–256 个可见 ASCII 字符，无空格，并在该环境所有群运营 Webhook 间唯一；不要在里面放手机号或其他私人信息。

```sh
# 本地检查，不读取密钥、不请求服务器、不创建发送任务。
aicrm-cli group-ops webhook send \
  --input groupops-request.json \
  --event-id groupops-20261014-live-001 \
  --dry-run

# 选定话术和目标后正式提交；可能创建真实企微群发任务。
aicrm-cli group-ops webhook send \
  --input groupops-request.json \
  --event-id groupops-20261014-live-001
```

`--dry-run` 只做本地结构和协议检查，不能证明计划已启用、目标绑定、服务权限、素材或 Provider 就绪；日课封面支持规则最终由服务端验证。可以在尚未配置密钥时使用。

`--input -` 从 stdin 读取完整 JSON。`--profile NAME`、`--timeout 30s`、`--format json|table|ndjson` 与原 CLI 一致。没有密钥、URL 或签名命令参数。

客户端自动取当前 Unix 时间戳，将 `timestamp + 换行 + event_id + 换行 + 原始 JSON 字节` 做 HMAC-SHA256，附加四个协议头后向当前 Origin 的固定群运营路径 POST。它不重新排版 JSON，不转发重定向，不自动重试写请求，也不把请求正文或签名输出。

## 6. 结果和投递查询

成功的退出码 0 和 HTTP 202 表示本地 run/EER 意图被受理。CLI 输出 `acceptance=accepted`、`delivery_proven=false` 和请求/事件摘要；保存 `data.run.run_id`、`data.executions[].execution_id` 以及本次输入文件和事件 ID。摘要可用来比对原请求，不替代运行记录。

之后查询原计划的执行记录：

```sh
aicrm-cli group-ops group-ops-executions list --param plan_id=12

# 有企微任务回执后读取该执行的真实投递状态。
# 此操作按原 Owner 合同读取 Provider 并更新投递证据，不创建新群发。
aicrm-cli group-ops read-group-ops-plan-execution-delivery invoke \
  --param plan_id=12 \
  --param execution_id=YOUR_EXECUTION_ID \
  --idempotency-key delivery-read-20261014-001
```

企微 `msgid` 表示群发任务已创建，仍须运营成员确认发送。最终核对对应群的投递结果；不能将受理、排队、Provider 任务创建视为群内送达。

## 7. 错误、结果未知与重放

CLI 普通成功或失败均输出一个 JSON 文档；失败为非零退出码，保留 HTTP 状态和安全的 Owner 错误码，不输出任意服务器错误正文、密钥或签名。

| 退出码 | 场景 | 处理 |
|---:|---|---|
| 1 | Owner 明确拒绝，例如 `provider_disabled`、封面暂不可用、运行时不可用 | 按原事件和正文保留现场，处理环境或素材问题后再核对 |
| 2 | 本地输入或 `invalid_request`、`plan_not_found`、封面形态不支持 | 核对描述符、请求和素材；修正消息内容后使用新的逻辑事件 |
| 3 | 缺密钥、Origin 不匹配、`protocol_authentication_failed` | 检查环境、密钥及本机时钟；不泄露签名 |
| 4 | 重定向拒绝 | 核对正确 HTTPS Origin；签名不会转发到其他地址 |
| 5 | `idempotency_conflict` 或 `operations_conflict` | 核对原事件、计划状态和绑定群，不自动换事件重发 |
| 7 | 超时、连接丢失、响应不完整/不可解析或非合同响应 | 结果未知，先读原计划执行记录，不自动重发 |

服务器时间窗口允许过去五分钟、未来一分钟。每次显式提交产生新时间戳，但保留事件 ID 和请求原始字节。相同事件、相同正文和同一 Webhook URL 按服务器既有幂等合同读回原 run；同事件不同正文或 URL 会冲突。

结果未知时保持原文件内容，包括空白、字段顺序和换行。先核对原运行记录；经确认需要恢复时再显式提交**同一个事件 ID 和字节完全相同的文件**。不要改 JSON 排版，也不要为了绕过冲突生成新事件，否则可能创建第二次真实发送。

## 8. 删除本机密钥及能力发现

```sh
aicrm-cli group-ops webhook forget
aicrm-cli group-ops webhook --help
aicrm-cli schema webhook
```

`forget` 只删当前 Origin 的本机 Webhook 密钥，不退出人类账号，不撤销服务器签名密钥；环境注入由注入方清除。服务器换密钥后按原部署治理同步各客户端。

`capabilities list/live` 继续表示服务端的 564 项登记目录（群运营 54 项管理接口）。新增的 configure/status/forget/send 是本地协议客户端命令，通过 `webhook --help` 和 `schema webhook` 查看；不把 HMAC 身份转换为人类或机器授权。

## 验证边界

发行验证覆盖签名、原始字节、重放冲突、凭据隔离、拒绝重定向、超时/响应丢失不重试、材料结构检查及五个平台构建。生产真实群发、运营成员确认和最终投递需要单独执行并记录；发布 CLI 或 dry-run 不提供这些业务证据。
