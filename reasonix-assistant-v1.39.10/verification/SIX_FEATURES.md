# Friday 六项生活提醒扩展

版本：`v1.39.10-assistant.34-six-features`。本轮仅实现统一作息、邮件转待办确认、睡前总结、截止事项跟进、订阅到期、自然固定提醒；天气预警监控、GitHub PR跟踪仍为设计项，日历继续暂缓。

## 使用与行为

- 周一/三/四09:50、周二/五08:00、周末11:00开始，均23:30结束；保留指定日期覆盖。固定钟点在作息外跳过，不顺延；主动查询全天可用。
- 原有邮件筛选和增量游标不变。对本来应通知的邮件，模型读完可用正文后可提出最多两个行动候选，宿主附简短询问。候选不是待办。控制器绑定真实用户消息和上一条提问，只有确认后才在同一SQLite事务内转换为待办、保存关联提醒与确认凭据。缺少精确时间继续询问；用户可明确只记不提醒。不能把后台邮件指令、模型自己的同意或合成续跑当成人的授权。
- 新建明确截止事项默认提前24小时与2小时提醒；过时或作息外的阶段返回跳过说明，不擅自挪动。完成、取消、暂停会停止关联提醒；修改截止重新生成有版本的计划，发送前检查最新任务与关联ID，旧计划不能继续投递。已提醒不是已完成，逾期不额外创建催促推送。
- 每天23:00发送睡前总结：剩余待办、明日最早课程/固定提醒、自然关照。无待办仍照常发送，课程缺失与无课明确区分。任务事实在最终组装时刷新，模型只写40字符以内的关照。模型失败时使用真实事实加保底关照；数据读取失败会报错而不假装无事。有效长期/短期记忆进入模型上下文，过期或归档记忆不恢复。每个日期稳定ID，通常在23:00后5分钟内可创建，10分钟内允许投递，仍受作息限制。
- `assistant_subscriptions`支持新建、查询、修改、续期及归档，真实到期日期和精确时间二选一。提前7天、1天、当天在相应日期作息开始提醒。续期必须给新到期日，计划版本变更、归档后旧任务失效；已送达阶段不因重启或仅修改名称而重发。不操作付款或厂商账户。
- 固定提醒的自然正文由模型在创建时写好，程序到点直接发，不在投递时调用AI；创建确认和投递正文分开。可选原生`time_aware`以执行时钟渲染，跨分钟重试跳过旧报时，正文不反复生成；普通`direct`使用准备好的正文，`verbatim`用于明确逐字转述。

## 主要接口和存储

- 复用`assistant_tasks`，新增`deadline_reminders`、`notification`；返回`reminders`、`reminder_warnings`。
- 新增`assistant_proposals`：list/get/approve/reject；消息证据来自宿主上下文，不接受工具参数伪造。
- 新增`assistant_subscriptions`：create/list/get/update/renew/archive。
- 新增`assistant_evening`：后台绑定snapshot，前台preview/latest；latest只包含平台确认送达的报告。
- 新模块`evening_briefing`与`subscription_reminder`仍由现有Heartbeat入口和统一作息门控执行。订阅使用Prepare生成原生message，不额外开常驻进程或轮询外部API。
- Todo兼容原JobID，增加ReminderJobIDs、ReminderRevision及SourceProposal；SQLite实体增量增加task_proposal、subscription、evening_snapshot/read/result/delivered及邮件片段哈希证据。未迁移或重写已有个人待办。
- `assistant expansion-enable`为本次发布的显式激活命令：保留旧触发器、模块开关、日期覆盖，只修改已批准周作息并添加晚间/订阅入口。它是管理命令，重复运行会重新应用已批准默认值，不在每次启动时强制覆盖用户后续调整。

## 验证

- 完整运行assistant、heartbeat、cli、boot、bot、weixin、config、tool相关包测试；控制器专项确认真实用户轮次与合成轮次隔离；assistant/heartbeat race检测。
- 新增回归覆盖邮件完整分页、伪造证据、查询未落库、非确认问候、后台写入拒绝、同意后原子创建、批准重放、延期/完成拦截旧提醒、作息边界、订阅续期与已发送去重、晚间事实刷新及AI降级。
- `scripts/six-features-acceptance.py`使用实际构建程序、隔离数据库、模拟模型及模拟微信：真实前台会话确认产生正式待办和两次截止提醒，订阅和固定提醒不调用模型，晚间快照及送达确认正常，长短期/过期记忆边界和重启不重发通过。邮件正文证据提取由Go测试验证，二进制测试使用合成候选；不声称接收了真实报名邮件。
- golangci-lint、repolint及邮件传输12项协议测试；未运行整个上游仓库的所有测试。未用真实模型评测每日关照的长期自然度，需实际使用观察。

构建、隔离验收和生产激活的精确结果分别见`six-features-build.json`、`six-features-acceptance.json`、`six-features-deployment.json`。部署脚本先备份数据库和persona，校验生产前版本与SHA；激活事务前后逐行验证只改变Heartbeat配置与新策略实体。失败降级仅恢复本次配置，在revision一致时隔离新模块/截止job并保留完整payload，避免用旧数据库覆盖新的用户交互。
