# CRM CLI 使用与交付范围

这是 AI-CRM 的独立 HTTP 命令行客户端。当前提供 **24 个导航入口的命令映射、现有管理接口的薄客户端、20 项既有机器 V1 操作及权限管理调用**。CLI 调用业务 Owner，不连接数据库或企微 Provider。

**登记与验收分别记录。** 编译目录含 549 项：497 项人类接口绑定、20 项机器绑定、32 项不可执行项。其中 31 项是未核验企微能力及另需业务合同的扩展，1 项旧签名接口不能使用人类会话；其机器 V1 替代操作独立授权。绑定只是源码契约；不证明路由已安装、资源授权、Provider 就绪或业务成功。

完整逐项目录见 [能力索引](docs/capability-index.md)，机器可读目录是 `internal/clicatalog/port/catalog.json`。

## 构建与环境

从 [GitHub Release](https://github.com/qianlan33333-png/aicrm-cli/releases/latest) 下载适合系统的压缩包，解压后将 `aicrm-cli` 放入 PATH。也可以从源码安装（Go 1.26.6 或更高）：

```sh
go install github.com/qianlan33333-png/aicrm-cli/cmd/aicrm-cli@v0.1.0
aicrm-cli profile add staging https://your-staging-origin.example
aicrm-cli profile use staging
```

服务必须安装本候选，才能使用新增 challenge/self-session/V2 discovery/can-i。既有接口的执行仍由其原角色、CSRF、资源与业务规则授权。

`profile` 只保存 HTTPS Origin，不产生权限。凭证保存在系统密钥环，精确绑定 Origin；不接受跨 Origin 凭证或转发重定向。密钥环不可用时不降级为明文文件。一个 Origin 当前保存一种身份，切换人类或机器需要重新登录。

`AICRM_CLI_CONFIG_DIR` 可指定非秘密配置目录。自动化可以从受保护环境提供 `AICRM_CLI_TOKEN` 与完全一致的 `AICRM_CLI_CREDENTIAL_ORIGIN`；该令牌的授予、有效期及 IP 限制仍由服务器判断。不要在命令参数中放密码、令牌或 client secret。

## 两类身份与发现

```sh
# 交互时密码隐藏输入；自动化使用受保护 stdin。
aicrm-cli auth login --username YOUR_EXISTING_USERNAME
aicrm-cli auth whoami
aicrm-cli doctor

# 独立机器身份，不能继承人类角色。
aicrm-cli auth login --client-id YOUR_CLIENT_ID --scope read --secret-stdin
aicrm-cli capabilities live
aicrm-cli capabilities list
aicrm-cli schema get customer.detail.get
aicrm-cli schema cli
aicrm-cli auth can-i customer.detail.get --resource customer-resource.json
```

`customer-resource.json` 示例：`{"customer_id":7}`。`can-i` 有三个结果：`allowed`、`denied`、`requires_resource_check`。最后一种也返回退出码 4，表示对象权限尚未证明；不会通过查询运行写操作。执行始终重新授权。人类 can-i 对无法从角色安全判定的动作保留 Owner 校验，不能把它当对象或字段权限的肯定证明。

`doctor` 查询当前身份并显示真实传输 Origin 和服务配置的 Origin；配置 Origin 不一致时返回非零。服务未报告 Origin 时明确未知。它不检查所有 Provider 条件，也不替代执行时授权。

`auth logout` 通过原接口撤销人类会话并移除本地凭证；机器退出只移除密钥环凭证，服务器禁用/轮换使用既有客户端治理操作。环境注入的令牌须由注入方清除。

## 命令、输入与输出

所有业务操作可以按目录中的 `group + command` 调用，或使用准确的登记 ID：

```sh
aicrm-cli access users list
aicrm-cli invoke customer.detail.get --param customer_id=7
aicrm-cli invoke customer.list --all
aicrm-cli invoke admin.confirmCustomerOwnerHandoff --input confirmation.json
aicrm-cli owners customer-owner-handoff-batch get --param batch_id=YOUR_BATCH_ID
```

先用 `schema get` 核对当前字段、版本和业务前置条件。修改所需 `expected_version`、现有确认语句、action token、名单预览及业务审批都沿 Owner 的原合同提交。

- `--param name=value`：已登记路径参数；`--query name=value`：业务查询字段。
  已声明的数组查询字段使用 JSON 数组，例如 `--query 'types=["message","order"]'`；仅按该字段的 explode 合同产生重复 URL 参数，普通重复参数仍拒绝。
- `--input FILE` 或 `--input -`：JSON/二进制文件或 stdin；重复 JSON 成员及重复参数拒绝。
- `--upload field=FILE`：登记的 multipart 上传，文件流式传输；分片文件继续使用原分片业务合同。
- `--idempotency-key KEY`：原合同要求的 HTTP 头。**JSON 内的 `idempotency_key` 仍须写在输入文件里**，不会隐式转换或覆盖。
- `--dry-run`：本地结构检查，不请求服务器、不确认授权或 Provider 条件；不是完整 JSON Schema/业务校验。
- `--output NEW_FILE`：下载到新的受保护文件，拒绝覆盖或符号链接；输出 JSON 说明文件位置。
- `--secret-output NEW_FILE`：客户端创建/轮换的一次性密钥响应必须保存到此文件，权限 0600，不打印密钥。
- `--unrestricted-owner-scope`：仅在需要明确全面机器范围时使用；创建仍需输入 `owner_scope:{}`，不能靠漏填。PATCH 省略范围保留原值，`null`/空对象扩大范围需要此明确声明；空数组拒绝。

机器客户端的 read/write scopes 与 capability 分开配置；OwnerScope 只使用当前领域能够解析的资源键。现有空 OwnerScope 的全面范围语义保留。CLI 的输入保护不替代服务器管理接口的授权和 presence-aware 更新合同。

非交互默认为 JSON。普通成功/失败输出各为一个 JSON 文档，失败保留业务结果及非零退出码。`--format table` 输出字段和值两列，嵌套值为 JSON；`--format ndjson` 输出逐行 JSON。提示走 stderr。

`--all` 只沿响应中明确存在的 `next_cursor` 前进，输出 NDJSON；不猜测页码或重试游标。无游标、重复游标或服务器拒绝旧游标时停止。它不为原接口制造新的授权游标合同。

`--wait` 只轮询已登记的 GET 状态操作，输出 NDJSON；使用 `--status-field` 对齐 Owner 状态字段。单纯受理可正常返回，但等待超时/待员工执行/仅技术执行不能作为业务完成。分页及等待不能与文件输出混用。

## 结果、异常与退出码

| 退出码 | 含义 |
|---:|---|
| 0 | 调用正常返回，或等待到该 Owner 的完成状态；仍须检查 Provider/员工/业务证据 |
| 1 | 传输读取、协议、业务失败或其他运行错误 |
| 2 | 参数/结构/分页协议不可用 |
| 3 | 认证缺失、失效或 Origin 不匹配 |
| 4 | 权限拒绝、对象权限尚未证明、候选不可执行或环境诊断不一致 |
| 5 | 版本或幂等冲突 |
| 6 | 等待超时、取消等待或只有执行证据 |
| 7 | 结果未知，须查询/对账原操作 |
| 8 | 部分完成，须查逐项结果 |

写请求不自动重试。超时、响应丢失、5xx 或下载的一次性密钥响应不完整时，先查询原业务记录/操作及审计；不要换幂等键重发。Provider 已执行、员工已执行和最终业务读回仍按领域分别记录。

## 服务端边界

当前目录保留 549 项元数据；目录存在不表示服务端已安装或 Provider 就绪。32 项没有执行绑定。机器身份只使用已登记的机器合同；人类管理接口仍由服务器授权。客户端不包含服务端、数据库、Provider、应用部署或账号恢复工具。目录更新需同步兼容服务端契约。
