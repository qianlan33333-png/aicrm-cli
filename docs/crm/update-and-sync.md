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

stable 使用一次公开 `HEAD https://github.com/qianlan33333-png/aicrm-cli/releases/latest` 的重定向获取稳定版，只比较 SemVer，不跟随跳转、不解析 HTML、不使用 GitHub REST API，也不自动降级。`--channel prerelease` 仍被识别，但在网络和文件操作前明确报错；不再自动挑选预发布，也不会静默回退到 stable。预发布需从发布页手工下载、解压和替换；`upgrade --target` 安装的是稳定版，不是把当前下载的预发布程序复制过去。`upgrade --download-only` 只下载并校验当前平台归档。

校验文件和平台归档直接从同一已选 tag 的 `releases/download/<tag>/` 下载，不查询资产列表；即使下载期间发布新版也不切换 tag。安装必须匹配 `checksums.txt` 中的 SHA-256，保留下载和解压大小限制。`update check` 保留现有结果结构，`release.assets` 为兼容保留为空数组，不提供 REST 资产 Size/digest。仅接受固定仓库及指定 GitHub 下载域，不附带 CRM token、Cookie 或密钥环凭据。五种发行平台为 Darwin arm64/amd64、Linux arm64/amd64、Windows amd64。不支持的平台明确报错。

二进制在原目录准备临时恢复备份并原子替换，随后锁内独立读回版本和文件摘要。确认成功后自动删除该安装收据关联的旧版本文件，返回 `rollback_available=false` 且不再保留 `rollback_backup`；`upgrade status` 仍可读回安装状态。下载准备、升级失败或版本不明时保留恢复材料。清理失败通过 `cleanup_warning` 明确报告，已确认的安装不被冒称失败，不自动重装或重放业务。失败保留原文件或回滚备份；`upgrade status` 根据实际摘要读回，未知结果停止下一次覆盖。`upgrade rollback` 仅用于仍保留恢复副本的失败/未确认安装；成功清理后会明确告知无旧版本备份可回滚，拒绝覆盖其他安装器改写的文件。不会迁移用户配置、登录状态或服务端数据，也不提权。安装目录无写权限或 Windows 正在运行的文件被锁定时，保留已校验归档并报告失败；可退出程序后按原安装方式替换。Windows 实际运行文件替换应在 Windows 上另行验证。

## Codex 业务前检查与接续

仅显式 `--context codex` 对在线业务、认证及 doctor 在执行前查询一次固定 GitHub 仓库的最新稳定 Release。1.5 秒超时、不重试、不复用六小时缓存；只比较 SemVer，本地更高或预发布版不降级。help、version、docs、schema、profile、离线 capabilities、completion、update 和 upgrade（含 status/rollback）免查，按解析后的命令路径判断，参数中的 version/auth 不影响分类。JSON、管道与非交互调用仍检查。开发构建 dev 不查询公开稳定渠道。

查询失败只在 stderr 提示，继续当前业务；不称已是最新。不使用 GET、REST 或其他信息源回退。公开网页和下载仍可能受网络、网站限流影响，这不是无限额度承诺。发现新版时，当前进程不读取业务 stdin、不获取凭据修改锁、不发 CRM 请求。macOS/Linux 原位安装并读取版本；Windows 仅准备已校验归档，退出后由 Codex 从临时新版更新原入口。结果退出码 9、error.code=update_required、result.business_executed=false；target、status、verified_archive 等字段说明实际结果。安装失败或状态不明也退出 9，不能据此直接重放。

Codex 只在明确收到上述未执行业务结果，且已通过原入口 version 确认达到目标版本后，最多重新调用原业务命令一次。第二次仍要求升级则停止。管道生产者、流式或一次性 stdin 不自动重跑，不缓存秘密输入；升级后重新提供输入。普通文件输入可重新读取。其他失败、超时及业务结果未知不得触发重放。CLI 不接力启动业务进程、不转交 stdin 或信号。

首次 v0.3 升级需下载新版；新版自身已最新也会处理指定旧目标：

```sh
/path/to/downloaded/aicrm-cli upgrade --target /path/to/partner/aicrm-cli
/path/to/partner/aicrm-cli version
/path/to/downloaded/aicrm-cli upgrade status --target /path/to/partner/aicrm-cli
/path/to/downloaded/aicrm-cli upgrade rollback --target /path/to/partner/aicrm-cli
```

目标符号链接首次解析到真实文件，后续不改写链接、不跟随新的链接指向。安装锁内重读目标版本，已相同或更高则不覆盖；未知版本目标下载期间变化则停止。Windows 从校验归档解压临时新版，在原进程退出后执行同一 --target 命令；仍被占用则报告失败，不使用自重命名恢复器。完成后从原入口读版本并删除本次临时下载，避免第二个长期入口。只操作指定入口，不扫描其他 PATH 安装，不修改 profile、钥匙串或登录状态。固定程序和伙伴入口物理共用一个文件时，首次接入先分开。

## 维护两个仓库

共享 CLI 源码、目录和服务端合同文档由 V4 拥有。独立仓库原有离线资料、企微签名 Webhook 和 README 由独立仓库保留，使用一个分发扩展入口。不要复制其他旧 CRM 仓库。

1. 提交 V4 行为和相关检查清单，运行 `dev_preflight.py prepare`；提交机械差异，用干净 checkout 执行 `prepare --verify`。
2. `python3 scripts/cli_sync.py export --cli-repo /path/to/aicrm-cli-worktree` 从已提交的 V4 导出，生成 `docs/crm-source.json`，明确来源 head/tree、每个受管文件和总摘要。目标有未完成改动或受管文件漂移时停止，不覆盖用户工作。
3. 在独立仓库运行原有与新增的 CLI 合同、构建和隔离安装/回滚。用 `cli_sync.py verify` 校验共享核心，提交并推送准确 CLI 候选分支。
4. `python3 scripts/cli_sync.py record-pushed --cli-repo /path/to/aicrm-cli-worktree --ref refs/heads/codex/WORK_ITEM` 通过已配置的固定仓库读取准确 ref，生成 `docs/cli-sync.json`。把收据提交回 V4；有效载荷摘要不会因提交收据而改变。
5. V4 handoff 校验该收据与当前 CLI 有效载荷匹配。实际改动共享 CLI、目录、CLI 文档、CLI 环境或客户端依赖时才要求同步；其他服务器变动不新增远端依赖。独立仓库推送、公开 Release、stable 切换和 CRM 安装分别记录，不能互相代替。

本批用户已授权将 Codex 授权隔离与更新能力合并发行一个稳定版；沿原 PR #2 与原队列交付，准确源码和构件由唯一发布工作台绑定。不独立发行旧候选、不安装 CRM 应用、不迁移数据库。真实扫码、桌面钥匙串及各 OS 原生安装验收分别记录。
