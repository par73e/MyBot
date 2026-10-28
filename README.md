# MyBot / Friday

基于 Reasonix `v1.39.10` 的云端私人助理，通过微信对话管理待办、定时提醒及长期/短期记忆，由常驻 Heartbeat 调度主动任务。

当前服务版本为 `v1.39.10-assistant.42-briefing-reliability`，模型配置为 `qwen3.8-flash`，提供 Low、Medium、XHigh 档位。日常上下文不自动加载部署环境与工作目录，技术工具通过按需入口使用。微信日常会话每天北京时间 06:00 切换、按日期命名；主动通知进入同一天会话，历史可以按需查询。和风天气查询与每日晨报已接入，支持按问题查询天气、空气质量和官方预警；AI News公开新闻源已接入初版，日历尚未接入。统一模块化 Heartbeat 已部署，支持通过 Agent 配置触发时间、选择天气／邮箱模块、修改指定日期的安排，以及独立的每周或单次模块调用；当前邮件每周重复：周一/三/四 09:48、周二/五 07:58、周六/日 10:58 开启，每天 23:30 关闭（北京时间）。起床时间仅触发一份“早安Friday”，按天气、课程、邮件、待办、AI News五段极简播报；旧天气更名为详细天气播报，天气与邮箱均不在起床时单独推送。天气工作日学校、周末家仅作为地点候选，Agent结合长期／短期记忆及本次问题决定实际查询地点；未来安排写入后即进入上下文，按行程日期使用。白天邮件增量检查保持原有时间和筛选规则。已支持从明确的自然表达保存长期偏好或有期限的短期安排；静默邮件汇总模块支持手动查看及自行配置定期运行，默认不额外添加汇总时间。微信已启用程序级最终答复过滤，详见[验证说明](reasonix-assistant-v1.39.10/verification/FINAL_ONLY.md)。

已发布统一作息门控、课表/教室与课前10分钟提醒，以及五段极简“早安Friday”。见[实现与验收](reasonix-assistant-v1.39.10/verification/GOOD_MORNING_FRIDAY.md)。

独立 AI News 模块已部署：主动查看默认提供七个不同事件，每条标题与摘要合计最多50字符（含标点）；来源、时间和原文链接单列，支持继续追问。早安Friday复用相同新闻采集与排序，只展示前三条，每条最多25字符，合在新闻那一行；独立新闻不再附来源状态说明或结尾引导；默认没有新增周期新闻推送。普通问答已修复末尾网址被拆成单独气泡的问题，见[发送分段修复](reasonix-assistant-v1.39.10/verification/URL_SAFE_REPLIES.md)。见[独立新闻验证](reasonix-assistant-v1.39.10/verification/STANDALONE_AI_NEWS.md)与[晨报新闻接入验证](reasonix-assistant-v1.39.10/verification/MORNING_SHARED_AI_NEWS.md)。

Friday 的通用表达规则已更新：按内容判断标题、段落与重点，新闻标题与正文分清层次，真实紧急事项突出风险和行动；晨报六行与新闻字数限制保留。见[表达与排版部署记录](reasonix-assistant-v1.39.10/verification/EDITORIAL_STYLE.md)。

六项生活提醒扩展：邮件行动候选先询问、确认后才保存待办与提醒；截止事项默认提前一天及两小时提醒，完成/取消/延期联动旧提醒；23:00固定睡前小结；旧订阅保留到期前七天、一天及当天提醒；新到期事项默认提前七天提醒一次，支持自定提前天数或指定日期；固定提醒在创建时写好自然措辞，到点直接发送。使用方式和验证边界见[六项扩展说明](reasonix-assistant-v1.39.10/verification/SIX_FEATURES.md)。

到期管理按事项性质区分物品到期与行动待办，支持指定提醒日；2026-10-28 已部署 assistant.41，并完成一项既有提醒的迁移。见[到期管理说明](reasonix-assistant-v1.39.10/verification/EXPIRY_MANAGEMENT.md)。

晨报与回复可靠性改进已部署，包含逐封邮件降级、候选数量提示和长标题保留，详见[验证与边界](reasonix-assistant-v1.39.10/verification/BRIEFING_RELIABILITY.md)。

## 当前版本

当前源码在 [`reasonix-assistant-v1.39.10/`](reasonix-assistant-v1.39.10/)。

2026-10-27 已将晨报已校验草稿恢复、个人记忆的当前用户原话校验、更自然的睡前表达、邮件通知统一送达去重及邮箱连续失败15次才提醒一并部署为 assistant.40，见[部署记录](reasonix-assistant-v1.39.10/verification/pending-assistant-deployment.json)。Follow Builders 已另外安装到云端 Friday 和本机 Codex，完成按需隔离试用；独立内容板块仍处于方案阶段，见[安装、实际输出与边界](docs/research/follow-builders/2026-10-27/README.md)。

- [私人助理使用说明与架构](reasonix-assistant-v1.39.10/ASSISTANT.md)
- [Friday 系统提示词](reasonix-assistant-v1.39.10/deploy/tencent/friday.md)
- [配置模板](reasonix-assistant-v1.39.10/deploy/tencent/assistant.example.toml)
- [核心模块测试报告](reasonix-assistant-v1.39.10/TEST_REPORT.md)
- [Friday 身份更新及验证](reasonix-assistant-v1.39.10/verification/FRIDAY_PERSONA_UPDATE.md)
- [Friday 日常上下文调整及验证](reasonix-assistant-v1.39.10/verification/FRIDAY_CONTEXT_UPDATE.md)
- [每日会话与主动任务验收](reasonix-assistant-v1.39.10/verification/DAILY_CONVERSATIONS.md)
- [模块开发收尾与验证](reasonix-assistant-v1.39.10/verification/MODULE_CLOSEOUT.md)
- [自然表达记忆与静默邮件汇总](reasonix-assistant-v1.39.10/verification/MEMORY_SILENT_DIGEST.md)
- [模块化 Heartbeat 与 Twitch 通知偏好](reasonix-assistant-v1.39.10/verification/MODULAR_HEARTBEAT.md)
- [地点身份、地区绑定与验收](reasonix-assistant-v1.39.10/verification/PLACE_IDENTITY.md)
- [天气选址与未来记忆调整](reasonix-assistant-v1.39.10/verification/WEATHER_CONTEXT.md)
- [天气查询、晨报与验收说明](reasonix-assistant-v1.39.10/verification/WEATHER.md)
- [邮件能力、验收与上线说明](reasonix-assistant-v1.39.10/verification/MAIL.md)
- [源码来源](reasonix-assistant-v1.39.10/SOURCE_PROVENANCE.md)

使用 Go `1.26.10` 在源码目录构建：

```sh
cd reasonix-assistant-v1.39.10
go build -ldflags '-X main.version=v1.39.10-assistant.40-mail-morning-memory' -o bin/reasonix ./cmd/reasonix
```

配置应使用独立的 `REASONIX_HOME`，启用 `REASONIX_ASSISTANT=1`。Friday 提示词放到该目录的 `prompts/friday.md`，配置参考上述模板。不要直接将模板覆盖到已配对的服务器配置。

## 历史版本

`reasonix/` 保留最初的 CLI Heartbeat 移植版本，供查看实现演进；新部署和后续修改应使用上面的当前源码。

- [早期 Heartbeat 使用说明](reasonix/docs/HEADLESS_HEARTBEAT.zh-CN.md)
- [早期源码补丁](heartbeat-v1.39.10.patch)
- [早期测试报告](HEARTBEAT_TEST_REPORT.md)
- [腾讯服务器使用说明](deploy/tencent/SERVER_GUIDE.zh-CN.md)
- [Low／Medium／XHigh 配置说明](deploy/tencent/REASONING_PRESETS.md)

Git 仓库不包含 API Key、微信登录凭据、生产会话、实际个人记忆数据库、编译产物或本地归档包。已有归档及 `BUILD.json`、`SHA256SUMS` 是对应交付版本的历史快照；Friday 更新的验证记录见上方链接。

根目录保留仓库原有的 Apache-2.0 许可证；Reasonix 源码沿用各源码目录中的 MIT 许可证及第三方许可说明。

晨报可靠性修复：完整草稿预检查并反馈具体错误，校验失败在同一次执行中恢复原会话修改；到达内部预算仍未成功时发送事实简版，未处理邮件交回白天检查。原作息、字数规则与投递去重保留，见[修复与验证](reasonix-assistant-v1.39.10/verification/MORNING_RELIABILITY.md)。

GitHub PR进度跟踪已接入：自动发现本人新开的公开PR，首次不回放历史；在作息内检查评论、审核、合并/关闭和当前提交CI失败/恢复，变化通知去重并保留原文链接。只读GitHub，不代发评论或操作CI。见[实现与验证](reasonix-assistant-v1.39.10/verification/GITHUB_PR_TRACKING.md)。

GitHub回复中文摘要改进（assistant.39）：只在新回复需要通知时调用模型，概括主要内容及具体修改要求并保留原文链接；轮询继续由程序完成，摘要失败仍发送中文进展提示。见[改进与验证](reasonix-assistant-v1.39.10/verification/GITHUB_PR_CHINESE_SUMMARY.md)，生产状态以[部署记录](reasonix-assistant-v1.39.10/verification/github-summary-deployment.json)为准。

上海官方天气预警模块已接入：作息内每5分钟原生检查中央气象台、中国天气网上海和全国预警数据。任一可信源发现有效新预警或升级即可通知，不等待多源共识；旧记录不覆盖新记录，空列表/失败不触发解除。程序持久化去重与投递回执，检查和预警通知不调用模型或和风付费接口。详细边界、故障与评测记录见[官方预警模块](reasonix-assistant-v1.39.10/verification/WEATHER_ALERTS.md)。
