# CLI 登录、期限和撤销

账号就是已有 CRM 管理账号。`auth login --username` 明确进入 CLI 认证入口，不依赖浏览器、User-Agent 或是否由 Agent 启动。密码只在本次 HTTPS 登录中使用，CLI 不保存密码、不自动续期、不自动重登。

| 入口 | 期限 | 每次请求的校验 |
|---|---|---|
| CLI 账号密码或企微扫码，当前全局白名单非空且来源匹配 | 无自动到期时间 | 账号、绑定/会话和 CLI 策略版本、原业务权限及当前来源 |
| CLI 账号密码或企微扫码，白名单为空或来源不匹配 | 登录起固定 24 小时 | 账号状态、会话和 CLI 策略版本、绝对期限及原业务权限 |
| 浏览器密码或企微扫码登录 | 登录起 8 小时 | 既有浏览器会话与业务授权 |

```mermaid
flowchart TD
 A[CLI 明确发起登录] --> B{选择登录方式}
 B -->|企微扫码| C[浏览器核对终端码和实际账号并确认]
 B -->|账号密码| D[既有密码认证]
 C --> E{CLI 来源匹配当前非空全局白名单?}
 D --> E
 E -->|是| F[长期 CLI 凭据]
 E -->|否| G[固定24小时 CLI 凭据]
 F --> H[保存并读回身份]
 G --> H
 H --> I[每次请求检查当前状态、版本和权限]
 F --> J[长期凭据额外校验当前来源]
```

这里的“长期”仍可撤销。退出、账号禁用、解绑、密码重置和策略变化沿用当前服务端规则。修改全局 CLI 白名单会使已有 CLI Token 的策略版本失效，影响全部账号，须显式重新登录；浏览器会话不因 CLI 策略变化而失效。白名单不是账号，也不识别 Agent；它决定本次登录是否具备长期凭据的来源条件，不扩大业务权限。

## 企业微信扫码登录（v0.4.0）

```sh
aicrm-cli profile add production https://your-crm.example
aicrm-cli profile use production
aicrm-cli auth login --wecom
# 远程终端复制链接，在你自己的浏览器中打开。
aicrm-cli auth login --wecom --no-browser
# 无钥匙串时，显式选择一个尚不存在的私有文件。
aicrm-cli auth login --wecom --no-browser --secret-output /private/path/crm-token.json
export AICRM_CLI_CREDENTIAL_FILE=/private/path/crm-token.json
aicrm-cli auth whoami
```

需要已绑定企微且获准登录的 CRM 员工账号。浏览器完成本次新鲜企微认证后，核对终端码和实际账号，明确确认授权；不要批准别人发来的登录链接。网页确认后仍需等待终端领取、保存和身份读回成功。授权窗口为 10 分钟，浏览器只具有批准能力，不能获取最终 CLI 凭据；终端只打印链接和核对码，不打印领取密钥或 Token。

新登录沿用上表期限、权限和撤销规则，没有自动续期或自动切换密码登录。失败时保留或恢复旧凭据，并关闭本次授权、撤销本次新会话。若撤销结果不明，明确报告 unknown，停止自动重试。CLI 凭据不能作为 RPA 账号验证所需的企微浏览器会话。

## 交互登录

```sh
aicrm-cli profile add production https://your-crm.example
aicrm-cli profile use production
aicrm-cli auth login --username YOUR_CRM_USERNAME
aicrm-cli auth whoami
aicrm-cli doctor
```

密码在终端隐藏输入。默认将返回的 Token 和 CSRF 凭据保存到系统密钥环；不在普通输出中打印。Origin 精确绑定，重定向不会转发凭据。旧版本保存的浏览器 Cookie 仍按原 8 小时期限使用；重新执行账号登录才会取得新 CLI Token。

## 没有桌面密钥环的服务器或 Agent

由调用方安全提供密码 stdin，并显式选择一个尚不存在的凭据文件：

```sh
aicrm-cli auth login --username YOUR_CRM_USERNAME --secret-stdin \
  --secret-output /private/path/crm-token.json < /run/secrets/crm-password
export AICRM_CLI_CREDENTIAL_FILE=/private/path/crm-token.json
aicrm-cli auth whoami
```

密码文件由调用方管理；CLI 不创建或保存它。Token 文件仅含 `origin`、`kind`、`token`、`csrf` 等凭据，不含密码；创建权限 0600，拒绝覆盖已有文件，加载时拒绝符号链接、非普通文件、其他用户可读文件和 Origin 不匹配。使用支持该权限模型的 macOS/Linux 文件系统；Windows 优先使用系统密钥环。

`AICRM_CLI_TOKEN` 与 `AICRM_CLI_CREDENTIAL_ORIGIN` 是已有独立机器身份注入入口，优先于凭据文件；账号 CLI Token 应使用上述文件或密钥环，不能把账号 Token 塞进机器身份变量。不要把密码或 Token 放进命令参数、截图、日志、Git 或普通输出。

24 小时到期会返回认证失败、退出码 3，CLI 不调用登录接口。Agent 若获准继续工作，需显式重新执行登录并保存新凭据；不能把原写请求自动重放当作续期。过期前后发生的写请求超时或结果未知，仍先查询原操作。

`auth logout` 撤销当前账号会话并移除本地凭据。撤销响应不明时会明确报告 `server_session_revocation:not_confirmed`；本地移除不能证明服务端撤销成功。

## 配置全局 CLI 白名单

只有现有 `super_admin` 可以读取/配置。使用全局入口；既有带账号 ID 的兼容入口也操作同一全局策略，不能作为单账号策略使用。

```sh
aicrm-cli docs operation admin.getGlobalAccessCLIPolicy
aicrm-cli invoke admin.getGlobalAccessCLIPolicy
```

白名单文件 `cli-policy.json` 示例（文档专用地址，换成实际服务器出口 IP）：

```json
{"allowed_cidrs":["203.0.113.20/32"]}
```

```sh
aicrm-cli invoke admin.setGlobalAccessCLIPolicy \
  --input cli-policy.json --dry-run
aicrm-cli invoke admin.setGlobalAccessCLIPolicy \
  --input cli-policy.json
```

响应包含规范化后的 `allowed_cidrs` 与只读 `version`。`version` 是撤销旧 CLI Token 用的服务端策略版本，不作为 PUT 的输入。

`allowed_cidrs` 必须明确提供数组；`[]` 表示新 Token 使用 24 小时，省略或 `null` 拒绝。支持 IPv4/IPv6 地址或 CIDR；单个地址由服务端规范化为 `/32` 或 `/128`。沿用现有规范，每次最多提交 20 项。可信代理解析由服务端配置控制，不接受调用方自行填写一个“允许的IP”。全局策略变化会使所有已有 CLI Token 失效；之后明确重新登录，命中白名单取得长期凭据，否则取得 24 小时凭据。

直接 HTTP 登录的响应字段：

| 字段 | 含义 |
|---|---|
| `access_token` | 账号 CLI Bearer 凭据，秘密；`acl_` 前缀表示用途，不是权限 |
| `token_type` | `Bearer` |
| `csrf_token` | 写操作仍须随 `X-CSRF-Token` 发送的秘密；CLI 自动处理 |
| `expires_at` | 固定 24 小时凭据的绝对截止时间；长期 Token 为 `null` |
| `automatic_renewal` | 固定 `false` |

不要把这些响应字段复制成浏览器 Cookie；CLI Token 无法作为浏览器 Cookie 登录。HTTP 入口为 `POST /api/admin/access/cli/login`，仅接收 `username`、`password`。不要把 CRM Token 与企微 `access_token` 混用。

独立的 `auth login --client-id` 保留原机器 OAuth 合同、scopes、capabilities 和资源范围。本次账号 CLI 的 24 小时/白名单规则不改变该合同；轮换某个机器 client secret 只影响相应机器客户端，不重置人类账号。

## 本次对抗性验证边界

| 攻击/边界 | 必须得到的结果 |
|---|---|
| CLI Token 当作浏览器 Cookie，或把浏览器 Token 当作 CLI Bearer | 拒绝用途混用；旧浏览器 Cookie 的兼容调用仍按原 8 小时期限 |
| 伪造代理头或换来源使用长期 Token | 使用既有可信代理链计算来源；非允许来源拒绝 |
| 修改/清空白名单后重放旧长期 Token | 拒绝旧策略版本 |
| 密码重置后重放旧账号 Token 或会话 | 拒绝旧账号版本 |
| 普通角色修改全局 CLI 策略 | 拒绝，不提升权限 |
| 24 小时到期、8 小时边界 | 按绝对期限拒绝，不滑动延长 |
| CLI 收到过期响应 | 返回退出码 3；不自动登录、不重发写操作 |
| 可被其他用户读取的文件或跨 Origin 文件 | 本地拒绝加载 |

源码模块、真实 PostgreSQL/TLS 旅程、实际安装二进制合同和生产读回分别保存收据；局部通过不称全仓回归。浏览器真实企微扫码和真实群发结果另外记录。

## 受管完整机器 CLI（v0.3.0）

独立机器可在服务端明确授予 `cli.admin.execute`、绑定有效管理授权、配置来源及企业范围后，用自己的机器 Bearer 调用已登记管理接口。客户端先读取在线上下文，再携带准确 `X-AICRM-CLI-Operation`；不读取或伪装人类 Token，不传 Cookie/CSRF 混用。读取需 read，管理写操作需 write，Owner 仍重新核验当前资源和业务条件。新客户端不自动授予这些权限。

已经获授读写的机器应请求 `--scope 'read write'`；请求 `read` 的 Token 仍只有读取权限，不会因为 IP 已加入白名单而自动增加写权限。机器 OAuth Token 保留服务端期限，自动化须由受管凭据注入方按既有合同重新签发；本次不引入自动重登或人类 Token 冒用。服务端未提供委托时明确拒绝。
