# CLI 登录、期限和撤销

账号就是已有 CRM 管理账号。`auth login --username` 明确进入 CLI 认证入口，不依赖浏览器、User-Agent 或是否由 Agent 启动。密码只在本次 HTTPS 登录中使用，CLI 不保存密码、不自动续期、不自动重登。

| 入口 | 期限 | 每次请求的校验 |
|---|---|---|
| CLI 账号，全局无 IP 白名单 | 登录起 24 小时 | 账号状态、会话撤销、当前全局来源策略、期限、原业务权限 |
| CLI 账号，全局有 IP 白名单 | 无自动到期时间 | 上述项目及来源 IP；清空名单后拒绝无绝对期限 Token |
| 浏览器密码/企微扫码登录 | 登录起 8 小时 | 原浏览器授权；扫码仍需真实 Provider 验证 |

期限不产生业务权限。全局名单适用于所有账号 CLI 请求，不能通过创建不同账号获得独立白名单。策略变化逐次影响来源判断，不把普通显示名、等价策略保存或审计版本变化当作所有 Token 撤销。已有短期 Token 仍受其原绝对截止时间约束，CLI 不自动续期或重放原写请求。

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

全局策略使用 `admin.getGlobalAccessCLIPolicy` / `admin.setGlobalAccessCLIPolicy`。必须具有服务端当前管理授权；受管机器还需明确的 capability、scope 和管理员委托，普通账号不能自行提权。旧带账号 id 的兼容路由也不表示个人白名单。

```sh
aicrm-cli docs operation admin.getGlobalAccessCLIPolicy
aicrm-cli invoke admin.getGlobalAccessCLIPolicy
aicrm-cli docs operation admin.setGlobalAccessCLIPolicy
aicrm-cli invoke admin.setGlobalAccessCLIPolicy --input cli-policy.json --dry-run
```

`cli-policy.json` 文档示例为 `{"allowed_cidrs":["203.0.113.20/32"]}`；仅在用户明确授权修改全局安全策略后执行真实写入。`allowed_cidrs` 必须明确为数组，省略或 null 拒绝。IPv4/IPv6 CIDR 及不带 zone 的裸 IP 由服务器规范化，实际输入预算以当前 schema 和服务器为准。响应 version 是策略审计版本，不作为 PUT 输入，也不证明所有 Token 已撤销。可信代理由服务器配置，调用方不能自报来源。每次请求按当前名单核对；空名单不接受无绝对期限 Token，已有短期 Token 继续受原截止时间和账号会话约束。

直接 HTTP 登录的响应字段：

| 字段 | 含义 |
|---|---|
| `access_token` | 账号 CLI Bearer 凭据，秘密；`acl_` 前缀表示用途，不是权限 |
| `token_type` | `Bearer` |
| `csrf_token` | 写操作仍须随 `X-CSRF-Token` 发送的秘密；CLI 自动处理 |
| `expires_at` | 无白名单时的绝对截止时间；长期 Token 为 `null` |
| `automatic_renewal` | 固定 `false` |

不要把这些响应字段复制成浏览器 Cookie；CLI Token 无法作为浏览器 Cookie 登录。HTTP 入口为 `POST /api/admin/access/cli/login`，仅接收 `username`、`password`。不要把 CRM Token 与企微 `access_token` 混用。

独立的 `auth login --client-id` 保留原机器 OAuth 合同、scopes、capabilities 和资源范围。本次账号 CLI 的 24 小时/白名单规则不改变该合同；轮换某个机器 client secret 只影响相应机器客户端，不重置人类账号。

## 本次对抗性验证边界

| 攻击/边界 | 必须得到的结果 |
|---|---|
| CLI Token 当作浏览器 Cookie，或把浏览器 Token 当作 CLI Bearer | 拒绝用途混用；旧浏览器 Cookie 的兼容调用仍按原 8 小时期限 |
| 伪造代理头或换来源使用长期 Token | 使用既有可信代理链计算来源；非允许来源拒绝 |
| 修改/清空白名单后重放长期 Token | 按当前来源策略判断；空名单拒绝无绝对期限 Token |
| 密码重置后重放旧账号 Token 或会话 | 拒绝旧账号版本 |
| 未授权角色修改全局 CLI 策略 | 拒绝，不提升权限 |
| 24 小时到期、8 小时边界 | 按绝对期限拒绝，不滑动延长 |
| CLI 收到过期响应 | 返回退出码 3；不自动登录、不重发写操作 |
| 可被其他用户读取的文件或跨 Origin 文件 | 本地拒绝加载 |

源码模块、真实 PostgreSQL/TLS 旅程、实际安装二进制合同和生产读回分别保存收据；局部通过不称全仓回归。浏览器真实企微扫码和真实群发结果另外记录。

## 受管完整机器 CLI（v0.3.0）

独立机器可在服务端明确授予 `cli.admin.execute`、绑定有效管理授权、配置来源及企业范围后，用自己的机器 Bearer 调用已登记管理接口。客户端携带准确 `X-AICRM-CLI-Operation` 调用原 Owner 路由，由服务端在同一请求校验当前委托、scope 和资源；需要发现能力时可显式读取在线上下文；不读取或伪装人类 Token，不传 Cookie/CSRF 混用。读取需 read，管理写操作需 write，Owner 仍重新核验当前资源和业务条件。新客户端不自动授予这些权限。

已经获授读写的机器应请求 `--scope 'read write'`；请求 `read` 的 Token 仍只有读取权限，不会因为 IP 已加入白名单而自动增加写权限。机器 OAuth Token 保留服务端期限，自动化须由受管凭据注入方按既有合同重新签发；本次不引入自动重登或人类 Token 冒用。服务端未提供委托时明确拒绝。
