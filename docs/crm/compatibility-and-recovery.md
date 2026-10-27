# CLI 兼容与恢复指引

| 组合 | 合同 |
|---|---|
| v0.6.0 → 新服务端 | 命令、参数、成功 JSON、分页和 error 类型保持；新增 error_detail 可忽略 |
| 新 CLI → 当前服务端 | 缺少 detail、非 JSON、未知原因、重复 JSON 键或超过 4096 字节时安全回退，不输出任意正文 |
| 新 CLI → 新服务端 | 展示登记原因、字段/字节数、恢复操作、可信诊断号和已知执行阶段 |
| 浏览器/脚本 → 新服务端 | error 仍为原字符串或各自原生协议对象；成功结构不改名 |
| 新旧程序 → 历史数据 | 原计划、内容版本、幂等摘要、结果快照可读；不删除历史收据 |

## 已允许的错误分类修正

- 小程序素材标题超 64 UTF-8 字节、用途非法、素材不存在或不可用：原通用 503 修正为 422；CLI 从 outcome_unknown/退出 7 变为 validation/退出 2。
- 创建计划同键业务不同：原漏映射的 503 修正为 409；CLI 从退出 7 变为 conflict/退出 5。
- 历史收据无法证明原请求一致：409，具体原因 legacy_receipt_requires_reconciliation，保留原计划查询依据。
- 机器认证的 scope/来源 IP/audience 拒绝明确为 403；凭据无效/过期/撤销为 401。真实依赖异常为 503，不伪装成无效凭据或输入错误。

HTTP 外层、原生 OpenAPI 和 JSON-RPC 原有错误外壳分别保留。JSON-RPC 沿原协议使用 error.data.error_detail，不将其他 HTTP 错误统一改成 200。未知原因不代表认证失效或业务成功。

## 失败后处理

401 重新认证；403 核对具体授权来源；400/422 修正字段；409 先读原对象和版本；429 按触发时的实际窗口/额度及 Retry-After 等待。静态手册的默认值不是实时额度。

写入连接断开、超时或无法证明是否接受，保留原键并查询原计划；不能因为可重试字样、上游读取失败或未观察到送达就自动重发。completed 是内部完成，provider_accepted 是上游接受，只有 delivery_proven 或核验过的 Provider 送达事实才证明送达。

## Token 与身份

机器在一次显式登录取得 Token 后复用系统钥匙串凭据：

```sh
aicrm-cli auth login --client-id YOUR_CLIENT_ID --scope 'read write'
aicrm-cli auth whoami
aicrm-cli doctor
aicrm-cli invoke admin.getAIAssistantPlan --param plan_id=YOUR_PLAN_ID
```

登录交互输入 secret；不要将凭据放入参数或日志。无终端时用现有 `--secret-stdin` 和受保护秘密来源。外部工作台也应缓存有效 Token，使用 expires_in 及真实 401 判断显式重新申请；CLI 不保存 client secret 或增加自动续期。已有环境注入 Token 必须绑定同一 Origin。账号 CLI 的受保护文件合同仍只接受人类凭据，不扩展为任意机器文件。

Codex 上下文的既有升级退出 9 表示本次业务未执行；升级未确认也不执行业务。不能重放管道或一次性 stdin。新版先兼容旧服务端，再由工作台服务端先行、客户端后续分发。

降级保留 v2 快照字段及全部数据，不恢复数据库、不删除收据；旧程序可能恢复旧错误表现，但不能据此重复接受或重建计划。
