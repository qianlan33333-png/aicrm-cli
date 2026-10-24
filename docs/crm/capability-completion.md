# v0.6.0：已上线管理能力进入 CLI

新增 48 个执行绑定，详见 [差集分类](capability-completion.json) 和 [编译目录](capability-index.md)。旧命令保持原样。服务端必须先安装本候选；命令存在不证明 Provider 已启用或业务已完成。

以下示例使用已配置的独立 Codex profile。`--input` 可传文件或 `-`（stdin）；写操作使用用户明确给出的原幂等键。示例中的 ID、版本、负责人均需先从授权读取取得，不能猜测。

```sh
aicrm-cli --context codex --profile my-crm invoke admin.listProducts --query limit=20
aicrm-cli --context codex --profile my-crm invoke admin.getProduct --param product_id=123
aicrm-cli --context codex --profile my-crm schema get admin.createProduct
aicrm-cli --context codex --profile my-crm invoke admin.createProduct --input product.json --idempotency-key YOUR_EXISTING_KEY

# 标签预览是可选读取；提交时重新验证当前权限与目标。
aicrm-cli --context codex --profile my-crm invoke admin.previewCustomerTagCommand --input tags.json --idempotency-key YOUR_EXISTING_KEY
aicrm-cli --context codex --profile my-crm invoke admin.acceptCustomerTagCommand --input tags.json --idempotency-key YOUR_EXISTING_KEY
aicrm-cli --context codex --profile my-crm invoke admin.listCustomerTagCommands --param customer_id=123

# 邀请保存、原计划读回、官方二维码保存。
aicrm-cli --context codex --profile my-crm invoke admin.saveGroupInvitationPlan --input invitation.json --idempotency-key YOUR_EXISTING_KEY
aicrm-cli --context codex --profile my-crm invoke admin.getGroupInvitationPlan --param id=123
aicrm-cli --context codex --profile my-crm invoke admin.downloadGroupInvitationQRCode --param id=123 --output new-qr.png

# 表格 POST 查询：在输入中显式传 config、limit 和下一页 cursor。
aicrm-cli --context codex --profile my-crm invoke admin.queryServicePeriodMemberGrid --param service_product_id=123 --input query.json
```

标签键可以来自原输入的 `idempotency_key` 或命令行；两个非空来源不一致时直接拒绝。会员的备注、视图和协作者修改必须使用当前版本。素材分组读取及图片成员读取可取得修改所需 ID/版本；附件、小程序沿用已有详情。订单回执必须显式指定 `--query provider=wechat_pay`（或 wechat、wechat_shop、no_payment、alipay），不跨渠道猜订单。

POST 标签预览和表格查询使用读取授权；GET 优惠券分享和邀请 QR 属于带副作用调用，委托机器必须有写入权限。优惠券分享带原 CSRF。QR 请求前先检查新输出文件；它不支持 `--all` 或 `--wait`。显式 offset/cursor，不自动翻 offset 页或重放写入。

邀请事务已提交但后续求值/读回失败时，退出 7，结果中保留 `plan_id`、`local_save_committed=true`。此时查询原计划；不要重新创建、换键或补偿删除。重复保存也可能遇到原 Owner 的后续版本冲突，同样保留已经保存的事实。其他结果未知、部分失败、排队和 Provider 执行状态按原回执分别判断；HTTP 202 仅表示受理。

旧 CLI 缺少新命令时应升级；不能删除 `--context codex` 回退认证。账号密码、机器密钥、扫码、独立凭据空间、每次版本检查和原入口更新沿用原规则。升级确认成功后仍会自动清理旧备份。
