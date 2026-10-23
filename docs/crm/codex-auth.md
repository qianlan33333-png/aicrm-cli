# Codex 桌面授权

每位用户先配置自己的 CRM 域名。新版可放在独立路径供 Codex 调用，固定程序继续使用原 CLI 路径和设置；不要用全局 upgrade 替换固定程序的二进制。

```sh
/path/to/codex/aicrm-cli --context codex profile add my-crm https://crm.example.com
/path/to/codex/aicrm-cli --context codex --profile my-crm auth login --wecom --no-browser
/path/to/codex/aicrm-cli --context codex --profile my-crm auth whoami
/path/to/codex/aicrm-cli --context codex --profile my-crm auth logout
```

所有业务命令使用相同 context、profile 和二进制路径。旧版本不识别 context 时停止，换用支持的新版本；禁止去掉参数重试。配置首次完成后不重复 add；不自动切换登录方式、自动创建新授权或续期。

## 给 Codex 的调用约定

1. 域名和二进制路径只在首次设置时解析。索要新链接时直接运行既定登录命令，使用约 1 秒的工具返回窗口。
2. stderr 出现已校验的授权 URL 与终端码后，立即结束本轮回复并交付链接和码，不先跑版本/源码/状态查询或整理记录。正常出链目标 3 秒，网络或钥匙串异常应如实报告。
3. 保持该有限 CLI 进程存活，保存工具会话句柄，不保存私有领取密钥。用户“已授权”后继续读取同一进程；退出码 0 且 authenticated=true 表示本次凭据已保存并读回身份。旧的 auth status 成功不证明本次请求成功。
4. 正常中断向该进程发 Ctrl-C，读取关闭结果再进行退出等修改。credential_busy 表示本空间本域已有修改进程，禁止 kill-all、删锁文件或改 context 绕过。
5. 进程丢失、超时、撤销结果不明时不自动重发。强杀/断电不保证执行关闭；未领取请求按原期限失效，已签发会话可能需要另行对账。只在用户明确再次请求后新建授权。
6. 401 引导一次登录；403 按权限或来源策略处理，不循环扫码。Token、领取密钥及 CSRF 不出现在回复或记录中。

## 隔离范围

不传 context 时行为保持原样。Codex 配置位于 OS 用户配置目录的 `aicrm-cli-codex/profiles.json`，钥匙串服务为 `aicrm-cli-codex-v1`；不读取旧 ConfigDirectory、Token、凭据文件、密码、机器密钥及绑定环境变量。空空间返回未登录，不借用旧身份。

隔离单位是空间和 Origin。同域多个 Codex profile 共用该空间身份，不能作为多账号切换功能。Codex 退出、扫码失败恢复和取消只操作当前空间/本次会话；账号停用等服务端全局撤销仍正常生效。本地空间不作为同一 OS 恶意用户的安全沙箱。

Codex 桌面要求可用的系统钥匙串；不支持 auth login --secret-output，不提供远程无钥匙串模式。密码及机器登录仍可显式交互或 --secret-stdin，独立签名 group-ops webhook 命令在 Codex 空间不可用。旧模式保留这些原有能力。会话仍按原 24 小时/白名单规则签发，不自动续期。
