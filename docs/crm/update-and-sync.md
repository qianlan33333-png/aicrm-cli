# CLI 版本、更新与同步

`aicrm-cli version` 输出客户端版本、客户端 commit、CRM 来源 commit 和内嵌目录摘要。客户端目录登记不证明服务器已经安装相应接口；业务执行继续使用服务端的权限、版本、幂等与 Provider 合同。

## 使用更新

```sh
aicrm-cli update check
aicrm-cli update check --refresh
aicrm-cli upgrade
aicrm-cli upgrade status
aicrm-cli upgrade rollback
```

普通交互终端成功执行命令后，至多每六小时检查一次固定仓库 `qianlan33333-png/aicrm-cli` 的 stable Release。发现新版本时只在 stderr 提示，默认选择不更新；输入 `y` 才安装。输入、stdout 或 stderr 被重定向，显式 JSON/NDJSON、stdin 业务输入、认证及更新命令不会出现自动提示。`AICRM_CLI_NO_UPDATE_NOTIFIER=1` 关闭自动检查；显式命令仍可使用。离线、超时及 GitHub 拒绝只影响更新检查，不改变原业务结果或退出码。

stable 排除草稿和预发布。仅显式 `--channel prerelease` 选择包括预发布在内的最高 SemVer 版本；不会因时间顺序或字符串排序误降级。`upgrade --download-only` 只下载并校验当前平台归档。

安装校验 `checksums.txt` 中的 SHA-256，以及 GitHub 返回时的资产 digest。仅接受固定仓库及指定 GitHub 下载域，不附带 CRM token、Cookie 或密钥环凭据。五种发行平台为 Darwin arm64/amd64、Linux arm64/amd64、Windows amd64。不支持的平台明确报错。

二进制在原目录准备、备份和原子替换，随后独立计算文件摘要。失败保留原文件或回滚备份；`upgrade status` 根据实际摘要读回，未知结果停止下一次覆盖。`upgrade rollback` 只恢复该次收据对应、摘要匹配的备份，拒绝覆盖其他安装器改写的文件。不会迁移用户配置、登录状态或服务端数据，也不提权。安装目录无写权限或 Windows 正在运行的文件被锁定时，保留已校验归档并报告失败；可退出程序后按原安装方式替换。Windows 实际运行文件替换应在 Windows 上另行验证。

## 维护两个仓库

共享 CLI 源码、目录和服务端合同文档由 V4 拥有。独立仓库原有离线资料、企微签名 Webhook 和 README 由独立仓库保留，使用一个分发扩展入口。不要复制其他旧 CRM 仓库。

1. 提交 V4 行为和相关检查清单，运行 `dev_preflight.py prepare`；提交机械差异，用干净 checkout 执行 `prepare --verify`。
2. `python3 scripts/cli_sync.py export --cli-repo /path/to/aicrm-cli-worktree` 从已提交的 V4 导出，生成 `docs/crm-source.json`，明确来源 head/tree、每个受管文件和总摘要。目标有未完成改动或受管文件漂移时停止，不覆盖用户工作。
3. 在独立仓库运行原有与新增的 CLI 合同、构建和隔离安装/回滚。用 `cli_sync.py verify` 校验共享核心，提交并推送准确 CLI 候选分支。
4. `python3 scripts/cli_sync.py record-pushed --cli-repo /path/to/aicrm-cli-worktree --ref refs/heads/codex/WORK_ITEM` 通过已配置的固定仓库读取准确 ref，生成 `docs/cli-sync.json`。把收据提交回 V4；有效载荷摘要不会因提交收据而改变。
5. V4 handoff 校验该收据与当前 CLI 有效载荷匹配。实际改动共享 CLI、目录、CLI 文档、CLI 环境或客户端依赖时才要求同步；其他服务器变动不新增远端依赖。独立仓库推送、公开 Release、stable 切换和 CRM 安装分别记录，不能互相代替。

本轮候选只在本地及原预发队列验收，不创建公开 Release，不改变 stable，不自动晋升生产。GitHub 连接失败时继续本地候选及测试，远端身份与推送收据保持待验证。
