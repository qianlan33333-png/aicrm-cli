# 群运营全部 CLI 能力（54 项管理接口及 5 项 Webhook 客户端命令）

本清单的编译目录同步 AI-CRM-v4 单任务多群修复提交 `46c4bf9537a84188dc78f28f8d157947a6409dde`。54 项均为已登记的人类管理接口绑定，执行身份为 human。机器身份不能继承这些管理接口权限。`v0.1.4` 提供以下专用 Webhook 客户端命令，使用独立 HMAC 身份。

| 完整命令 | 能力 |
|---|---|
| `aicrm-cli group-ops webhook configure` | 隐藏输入或 protected stdin 配置 Origin 绑定签名密钥 |
| `aicrm-cli group-ops webhook status` | 检查本机 Webhook 配置，不查询服务器 |
| `aicrm-cli group-ops webhook forget` | 删除当前 Origin 的本机 Webhook 密钥，保留人类登录 |
| `aicrm-cli group-ops webhook push` | 计划 ID＋话术＋已有小程序，自动分页读取全部绑定群、保存固定快照，向新版服务器提交一个多群任务 |
| `aicrm-cli group-ops webhook send` | 签名提交动态群运营消息，支持 `--dry-run`，不自动重试 |

完整示例、参数、素材准备、事件重放和投递查询见 [Webhook 使用说明书](groupops-webhook.md)。协议发现使用 `aicrm-cli schema webhook`。

群运营支持标准计划及 Webhook 计划、运营人员选择、群绑定、消息/延迟节点、内容包、执行受理、执行回执与历史查询。绑定只说明客户端能够构造请求；服务端权限、资源条件、Provider 就绪和真实投递结果须分别检查。

## 最新业务行为

- 先选择运营人员，再按该人员查看其群；支持搜索、分页及总数。已绑定群返回可用群名称，人数无来源时保持未知。
- 草稿或暂停状态可编辑绑定；运行中的计划需先暂停，完成编辑后再显式启用。更新沿用 `expected_revision` 或内容包 `expected_version` 的并发检查。
- 消息节点支持文本和素材引用；延迟节点使用 `delay_minutes`。内容包引用类型包括图片、附件、小程序及入群邀请。
- 预览到期任务不会创建发送任务；执行受理返回排队状态，不能视为已投递。投递状态读取和对账仍可能访问 Provider。
- 群目录刷新会受理企业范围目录扫描，再按运营人员展示。它与读取缓存目录不同；本次同步没有执行这些刷新命令。
- 历史计划、群目录、群和节点为只读事实，不作为当前发送任务执行。

## 调用方法

完整命令列已含 `aicrm-cli` 前缀。也可用 `aicrm-cli invoke 操作ID`。参数结构以 `aicrm-cli schema get 操作ID` 为准。路径参数用 `--param`，查询字段用 `--query`，JSON 用 `--input FILE`；需要幂等头时用 `--idempotency-key`。JSON 内的幂等字段仍按原业务合同填写。

```sh
aicrm-cli auth whoami
aicrm-cli doctor
aicrm-cli group-ops group-ops-plans list
aicrm-cli group-ops group-ops-operation-members list --query scope=group_ops
aicrm-cli group-ops group-ops-directory-groups list --query owner_userid=YOUR_STAFF_ID --query limit=20
aicrm-cli group-ops group-ops-plan get --param plan_id=YOUR_PLAN_ID
aicrm-cli group-ops group-ops-plan-groups list --param plan_id=YOUR_PLAN_ID
aicrm-cli group-ops group-ops-plan-nodes list --param plan_id=YOUR_PLAN_ID
aicrm-cli schema get admin.acceptGroupOpsRunDue
```

## 全部命令

### 计划管理（11 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询计划列表 | `aicrm-cli group-ops group-ops-plans list` | `admin.listGroupOpsPlans` | GET |
| 查询计划详情及当前版本 | `aicrm-cli group-ops group-ops-plan get` | `admin.getGroupOpsPlan` | GET；路径 plan_id |
| 创建计划 | `aicrm-cli group-ops group-ops-plan create` | `admin.createGroupOpsPlan` | POST；JSON 必填 |
| PATCH 更新计划名称、负责人或类型 | `aicrm-cli group-ops group-ops-plan update` | `admin.updateGroupOpsPlan` | PATCH；路径 plan_id；JSON 必填 |
| PUT 更新计划 | `aicrm-cli group-ops put-group-ops-plan invoke` | `admin.putGroupOpsPlan` | PUT；路径 plan_id；JSON 必填 |
| 激活计划 | `aicrm-cli group-ops group-ops-plan activate` | `admin.activateGroupOpsPlan` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 启用计划 | `aicrm-cli group-ops group-ops-plan enable` | `admin.enableGroupOpsPlan` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 暂停计划 | `aicrm-cli group-ops group-ops-plan pause` | `admin.pauseGroupOpsPlan` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 停用计划 | `aicrm-cli group-ops group-ops-plan disable` | `admin.disableGroupOpsPlan` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 归档计划 | `aicrm-cli group-ops group-ops-plan archive` | `admin.archiveGroupOpsPlan` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 删除入口，按服务端现有生命周期规则处理 | `aicrm-cli group-ops group-ops-plan delete` | `admin.deleteGroupOpsPlan` | DELETE；路径 plan_id；JSON 必填；幂等头必填 |

### 运营人员与群目录（7 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询可选运营人员 | `aicrm-cli group-ops group-ops-operation-members list` | `admin.listGroupOpsOperationMembers` | GET；必填查询 scope |
| 刷新运营人员目录 | `aicrm-cli group-ops sync-group-ops-operation-members invoke` | `admin.syncGroupOpsOperationMembers` | POST；JSON 必填；幂等头必填 |
| 按运营人员查询群目录，支持搜索、分页及总数 | `aicrm-cli group-ops group-ops-directory-groups list` | `admin.listGroupOpsDirectoryGroups` | GET |
| 查询群选择器候选项 | `aicrm-cli group-ops group-ops-group-picker list` | `admin.listGroupOpsGroupPicker` | GET |
| 请求群目录刷新 | `aicrm-cli group-ops sync-group-ops-directory-groups invoke` | `admin.syncGroupOpsDirectoryGroups` | POST；JSON 必填；幂等头必填 |
| 群目录刷新兼容入口 | `aicrm-cli group-ops sync-group-ops-directory-groups-alias invoke` | `admin.syncGroupOpsDirectoryGroupsAlias` | POST；JSON 必填；幂等头必填 |
| 群选择器刷新入口 | `aicrm-cli group-ops sync-group-ops-group-picker invoke` | `admin.syncGroupOpsGroupPicker` | POST；JSON 必填；幂等头必填 |

### 计划人员绑定（3 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询计划绑定人员 | `aicrm-cli group-ops group-ops-plan-members list` | `admin.listGroupOpsPlanMembers` | GET；路径 plan_id |
| 添加计划绑定人员 | `aicrm-cli group-ops group-ops-plan-member add` | `admin.addGroupOpsPlanMember` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 移除计划绑定人员 | `aicrm-cli group-ops group-ops-plan-member remove` | `admin.removeGroupOpsPlanMember` | DELETE；路径 plan_id, staff_id；JSON 必填；幂等头必填 |

### 计划群与群资产绑定（6 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询计划已绑定群 | `aicrm-cli group-ops group-ops-plan-groups list` | `admin.listGroupOpsPlanGroups` | GET；路径 plan_id |
| 添加计划群绑定 | `aicrm-cli group-ops group-ops-plan-group add` | `admin.addGroupOpsPlanGroup` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 移除计划群绑定 | `aicrm-cli group-ops group-ops-plan-group remove` | `admin.removeGroupOpsPlanGroup` | DELETE；路径 plan_id, chat_id；JSON 必填；幂等头必填 |
| 查询计划绑定群资产 | `aicrm-cli group-ops group-ops-plan-group-assets list` | `admin.listGroupOpsPlanGroupAssets` | GET；路径 plan_id |
| 添加群资产引用 | `aicrm-cli group-ops group-ops-plan-group-asset add` | `admin.addGroupOpsPlanGroupAsset` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| 移除群资产引用 | `aicrm-cli group-ops group-ops-plan-group-asset remove` | `admin.removeGroupOpsPlanGroupAsset` | DELETE；路径 plan_id, asset_reference；JSON 必填；幂等头必填 |

### 消息及延迟节点（5 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询节点列表 | `aicrm-cli group-ops group-ops-plan-nodes list` | `admin.listGroupOpsPlanNodes` | GET；路径 plan_id |
| 新增消息或延迟节点 | `aicrm-cli group-ops group-ops-plan-node add` | `admin.addGroupOpsPlanNode` | POST；路径 plan_id；JSON 必填；幂等头必填 |
| PATCH 更新节点顺序、文本、素材或延迟 | `aicrm-cli group-ops group-ops-plan-node update` | `admin.updateGroupOpsPlanNode` | PATCH；路径 plan_id, node_id；JSON 必填；幂等头必填 |
| PUT 更新节点 | `aicrm-cli group-ops put-group-ops-plan-node invoke` | `admin.putGroupOpsPlanNode` | PUT；路径 plan_id, node_id；JSON 必填；幂等头必填 |
| 移除节点 | `aicrm-cli group-ops group-ops-plan-node remove` | `admin.removeGroupOpsPlanNode` | DELETE；路径 plan_id, node_id；JSON 必填；幂等头必填 |

### 内容预览与内容包（8 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 预览并校验计划内容 | `aicrm-cli group-ops group-ops-plan-content preview` | `admin.previewGroupOpsPlanContent` | POST；路径 plan_id |
| 创建内容包 | `aicrm-cli group-ops group-ops-content-package create` | `admin.createGroupOpsContentPackage` | POST；JSON 必填；幂等头必填 |
| PUT 更新内容包 | `aicrm-cli group-ops group-ops-content-package update` | `admin.updateGroupOpsContentPackage` | PUT；路径 package_id；JSON 必填；幂等头必填 |
| PATCH 更新内容包 | `aicrm-cli group-ops group-ops-content-package patch` | `admin.patchGroupOpsContentPackage` | PATCH；路径 package_id；JSON 必填；幂等头必填 |
| 创建内容包版本 | `aicrm-cli group-ops group-ops-content-package-version create` | `admin.createGroupOpsContentPackageVersion` | POST；路径 package_id；JSON 必填；幂等头必填 |
| 预览内容包 | `aicrm-cli group-ops group-ops-content-package preview` | `admin.previewGroupOpsContentPackage` | POST；JSON 必填 |
| 绑定活动、内容计划、内容包与入群邀请 | `aicrm-cli group-ops group-ops-content-package bind` | `admin.bindGroupOpsContentPackage` | POST；JSON 必填；幂等头必填 |
| 查询内容包绑定 | `aicrm-cli group-ops group-ops-content-package-binding get` | `admin.getGroupOpsContentPackageBinding` | GET；路径 campaign_code, content_plan_id |

### Webhook 接入配置（3 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 读取计划 Webhook 配置入口 | `aicrm-cli group-ops group-ops-webhook get` | `admin.getGroupOpsWebhook` | GET；路径 plan_id |
| 读取 Webhook 描述符 | `aicrm-cli group-ops group-ops-webhook-descriptor get` | `admin.getGroupOpsWebhookDescriptor` | GET；路径 plan_id |
| 写入 Webhook 引用配置 | `aicrm-cli group-ops put-group-ops-webhook-descriptor invoke` | `admin.putGroupOpsWebhookDescriptor` | PUT；路径 plan_id；JSON 必填；幂等头必填 |

### 执行预览与受理（2 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 预览到期执行数量、阻碍与下一执行时间 | `aicrm-cli group-ops group-ops-run-due preview` | `admin.previewGroupOpsRunDue` | POST；路径 plan_id |
| 受理到期任务，产生排队记录 | `aicrm-cli group-ops accept-group-ops-run-due invoke` | `admin.acceptGroupOpsRunDue` | POST；路径 plan_id；幂等头必填 |

### 执行记录、投递状态与对账（5 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询计划执行记录 | `aicrm-cli group-ops group-ops-executions list` | `admin.listGroupOpsExecutions` | GET；路径 plan_id |
| 读取指定执行的 Provider 投递状态 | `aicrm-cli group-ops read-group-ops-execution-delivery invoke` | `admin.readGroupOpsExecutionDelivery` | POST；路径 execution_id；幂等头必填 |
| 按计划与执行读取 Provider 投递状态 | `aicrm-cli group-ops read-group-ops-plan-execution-delivery invoke` | `admin.readGroupOpsPlanExecutionDelivery` | POST；路径 plan_id, execution_id；幂等头必填 |
| 对指定执行进行对账 | `aicrm-cli group-ops group-ops-execution reconcile` | `admin.reconcileGroupOpsExecution` | POST；路径 execution_id；JSON 必填；幂等头必填 |
| 按计划与执行进行对账 | `aicrm-cli group-ops group-ops-plan-execution reconcile` | `admin.reconcileGroupOpsPlanExecution` | POST；路径 plan_id, execution_id；JSON 必填；幂等头必填 |

### 历史查询（4 项）

| 能力 | 完整命令 | 操作 ID | 请求要求 |
|---|---|---|---|
| 查询历史计划 | `aicrm-cli group-ops group-ops-history-plans list` | `admin.listGroupOpsHistoryPlans` | GET |
| 查询历史群目录 | `aicrm-cli group-ops group-ops-history-directory list` | `admin.listGroupOpsHistoryDirectory` | GET |
| 查询历史计划绑定群 | `aicrm-cli group-ops group-ops-history-groups list` | `admin.listGroupOpsHistoryGroups` | GET；路径 plan_id |
| 查询历史计划节点 | `aicrm-cli group-ops group-ops-history-nodes list` | `admin.listGroupOpsHistoryNodes` | GET；路径 plan_id |

## 核验范围

该清单完整覆盖 54 项已登记群运营 CLI 命令，包括兼容别名，不代表 54 项全部执行过真实写验收。客户端合同测试验证请求、认证/CSRF、幂等与响应处理；本机登录后另行核验真实服务读取。2026-10-15 使用 v0.1.4 实际提交计划 12 的话术和小程序素材 1195，run 2 只有一个执行及一个企微群发任务，覆盖 11 个群；Provider 逐群结果均为未发送，未进行员工确认。详见说明书。企业群目录扫描及其他配置修改未执行。
