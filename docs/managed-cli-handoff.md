# 受管完整 CLI 的能力发现和审核交接

API Origin 保留 HTTPS 域名，私网仅决定连接地址；来源白名单不代替操作授权。机器使用独立 OAuth，人工使用独立账号 CLI 会话。禁止把人工 Token 写入机器配置。

## 发现完整能力

`aicrm-cli capabilities live` 返回两组入口：

- `operations`：原生机器 API/MCP 操作。
- `delegated_operations`：当前 Token 具备 scope 且机器已明确获授 `cli.admin.execute` 的原管理命令，包含 operation ID、可用 CLI 命令、方法/路径和幂等要求。

两组共同构成受管 CLI 的在线发现；只枚举 `/open/v1/capabilities` 的原生操作会漏掉管理能力。该 V1 结果的 `cli_context_path` 指向完整发现。`capabilities list` 是编译目录，`schema get OPERATION_ID` 是本地合同，均不能单独证明当前授权。

`auth can-i OPERATION_ID` 返回 `requires_resource_check` / exit 4 时，含义为该入口已具备基础授权，最终还需 Owner 按当前资源、版本、原稿/素材及 Provider 配置核验。不能将此判定解释为登录失败或能力不存在，也不能当作实际批准成功。`denied`、401、403、400、409、503 分别保留其真实含义，禁止用重新创建来推进后续步骤。

## 对已有唯一实例继续交接

先读取原交接中的 `operation_id=ai_review_plan:<plan_id>`，不再次创建或生成新键。

| 环节 | 注册操作 | 必需事实与结果 |
| --- | --- | --- |
| 计划回读 | `admin.getAIAssistantPlan` | 当前 version/state/dispatch_ready |
| 逐人列表 | `admin.listAIAssistantRecipients` | 分页读完，不用客户昵称推断目标 |
| 原稿与卡片 | `admin.getAIAssistantRecipient` | 当前收件人 version、content digest、冻结文本和素材 |
| CRM 逐人审核 | `admin.reviewAIAssistantRecipient` | 独立业务审核授权、原稳定审核键、expected_version、decision |
| 批准预演 | `admin.previewAIAssistantPlanApproval` | 最新计划 expected_version；只读返回 eligible_count/preview_digest |
| 批准 | `admin.approveAIAssistantPlan` | 独立批准授权、稳定批准键、最新 expected_version/preview_digest；可能立即进入发送 Worker |
| 本地效果 | `admin.listAIAssistantEffects` | 区分 queued/attempted/provider_accepted/outcome_unknown/delivery_proven |
| 企微回执 | `admin.listAIAssistantExecutionReceipts` | GET `live=true`；绑定冻结计划/收件人，读取原任务，不重发 |
| 控制回读 | `admin.getAIAssistantRecipientControl` | 原 action/effect 的对账结果；仅查询不等同 stop/reconcile 命令 |

读取例：`aicrm-cli invoke admin.getAIAssistantPlan --param plan_id=<原计划号>`。请求形状使用 `schema get <操作>`；逐人操作还需 `--param recipient_id=<原收件人号>`。审核改变版本后重新读计划并预演，不沿用旧摘要。创建键、逐人审核键、批准键各属于原操作，持久保存并原样重试；不可共用一个键后更改命令内容。

已有 `source_kind=scrm_workbench` 包仍保留原暂停发送门禁；完整权限不绕过它，也不改变普通数字 recipients 格式。现有普通审核计划包含 pending_review 目标时，预演可能仍返回可批准人数：预演成功只是读取可批准事实，本身不会执行逐人审核或批准。批准是整个当前可批准集合的实际业务动作，不应自动执行。

## 结果口径

创建审核和登记推送记录都不证明 Provider 已接受。批准提交一个本地意图/EER 也不证明手机待发送。只有冻结任务的企微回执能证明 Provider 接受与手机发送状态；手机最终发送、会话存档再分别确认。结果未知保持原键只读对账，不自动换键重发。旧消费者继续保留原互斥交接，不因补齐发现而重新入队。

生产诊断应按已获授权范围只读原实例；本版完整审核/批准连续回归已在隔离数据库与已安装构件中执行。真实业务推进需要原业务授权，生产技术部署授权不能替代它。
