# MyBot 私人助理 v1.39.10-assistant.1

基于 Reasonix v1.39.10（195fc08132d253270de7d88afc8d395e3f96083e），包含此前的 CLI Heartbeat 移植与本次私人助理模块。源码、构建、测试在腾讯服务器完成。默认使用 qwen3.8-flash / Medium，Low、Medium、XHigh 配置保留。

## 本批范围

- 个人待办：保存、查看、更新、完成、取消、暂停、恢复、延期；可独立记录截止时间。
- 定时提醒：一次性、持续间隔、每日/每周和 cron；支持结束时间、时区、仅完成今天这一轮。
- 长期记忆：可读 Markdown。
- 短期记忆：自定义生效和失效时间，每次上下文加载实时过滤。
- Heartbeat：后台常驻检查，固定消息与 AI 分析独立执行；AI 分析读取当前记忆和待办，判断通知或静默。
- 微信对话调用两个受限工具管理上述状态；工具成功保存后才可以承诺已创建。

邮箱、天气、新闻按本次确认暂缓接入。自动分析任务若要求读取这些未接入的数据源，应说明缺少连接，不能编造查询结果。

## 结构和数据

代码：
- internal/assistant：SQLite 仓库、待办及调度事务、Markdown 记忆、模型工具、当前上下文。
- internal/heartbeat：持久调度、两条执行通道、结构化通知协议、发送和失败记录。
- internal/boot/assistant.go：可信宿主绑定工具、收件人与工作区。
- internal/control/session_context.go：每个对话回合动态刷新上下文。
- internal/cli/assistant.go：状态检查；heartbeat_bot.go 与 heartbeat_runner.go 负责后台调用。

持久数据位于 REASONIX_HOME/assistant：
- state.sqlite3：待办和对应的 Heartbeat 任务、执行与发送状态。新建提醒在同一事务中写入待办和触发任务。
- memory/<随机ID>.md：JSON/YAML 兼容的 front matter + 正文；kind=long/short、revision、starts_at、expires_at、archived。失效/归档内容保留可查历史，但正文不会进入当前有效记忆。上下文附带最多 128 条不含正文的失效状态标记，并明确最新有效列表优先于旧对话，避免从旧聊天恢复已过期偏好。
- *.lock：跨进程状态/执行锁；请勿运行时手工删除锁。

内容更新要求当前 revision，防止聊天和调度并发时覆盖新状态。重复的有效内容会合并。相同标题但不同时间的提醒属于不同任务。待办完成和提醒发送分别记录：发出提醒不会自动标记你已完成这件事。

## 运行过程

1. 微信消息 -> 已有 allowlist 校验 -> Agent 当前回合。
2. 宿主加载当前时间、有效长期/短期记忆、未关闭待办。
3. 模型根据意图调用 assistant_tasks 或 assistant_memory；程序校验时间、版本、范围，成功后持久化。
4. Heartbeat 每秒检查。检查本身不调用模型。
5. reminder 直接发送固定消息，不产生模型 token；automation 启动独立只读 Agent 进程。
6. 自动分析加载相同有效上下文和该任务的最近执行结果，输出：
   {"notify":false,"message":"","reason":"没有需要通知的新变化"}
   或 {"notify":true,"message":"需要告诉用户的内容","reason":"有新的可行动信息"}。
7. 只有经过协议校验的 notify=true 内容才发送。无效格式记为错误；notify=false 不发送。
8. 已计算出的发送失败可只重试发送，不重新调用模型；失败后按 10 秒起步递增间隔，最多 5 分钟，达到 8 次失败停止自动重试；部分发送或结果不确定时停止自动重发，避免重复。

取消会阻止后续调度；已经进入微信发送请求的消息无法撤回。

固定消息与 AI 分析各有一条跨进程执行通道，较慢的模型不会挡住固定提醒。同一通道串行。检查精度约 1 秒，最终送达还受调度负载、网络和微信接口影响，不承诺硬实时。

重启后一次性任务不会因已执行而重复运行；中途崩溃的 running 状态会标记 interrupted 并要求检查，避免盲目重放。离线错过的一次性任务可以在恢复后补执行，但不提供严格 exactly-once 的外部发送承诺。cron 规则按匹配分钟触发，停机错过的 cron 分钟不追补；每日/每周 calendar 规则可补最近一个计划时点。重要固定日程优先使用 daily/weekly calendar 格式。

## 说话示例

- “今天下午五点提醒我交报告。”
- “未来七天每天下午三点提醒我吃药，到下周四零点结束。”（涉及具体日期不清时会询问）
- “明天和周五早上八点提醒我出门。”
- “把刚才交报告的提醒改到六点。”
- “今天这一次已经做完了，明天继续提醒。”
- “取消这一周的吃药提醒。”
- “记住我喜欢简短的中文回复。”
- “今天少给我推送邮箱相关的信息，明天恢复。”（记录为今天到次日零点的短期偏好；邮箱本批未接入）
- “从现在开始，未来三天我在日本旅行；三天后这条情况失效。”
- “查看我当前有效的记忆和未完成待办。”

明确的提醒时间写入待办和调度，不依赖记忆召回。临时偏好由模型在分析时综合判断；本次不加入每个业务单独的硬通知规则引擎。仅说“记住我在旅行”而没说明结束边界时，不应猜成永久事实。

## 配置和边界

Friday 身份使用 `deploy/tencent/friday.md`，部署到 `REASONIX_HOME/prompts/friday.md`，由 `[agent].system_prompt_file` 加载。配置中另保留简短的 Friday 身份作为文件缺失时的回退。助手模式下使用个人助理执行规范，通用编程模式保持原有规则。助手重新打开已有会话时会刷新模型侧基础提示词，保留原始聊天记录和消息 ID；旧的编程身份不会因会话恢复而继续成为系统身份。真实上游验收脚本为 `scripts/check-friday-persona.py`，只操作临时隔离状态。

assistant.3 的日常上下文不自动加载部署环境、工作目录或已安装工具清单。重开旧会话时，仅在模型上下文中移除历史宿主快照的环境/工作目录字段，保留记忆、技能和原始聊天记录；通过稳定消息 ID 核对宿主来源，不修改用户自行写入的技术内容。常用助理工具直接展示；文件、命令和代码工具保持注册及原权限，通过 `use_capability` 按需调用，不作为日常能力菜单。

启用：REASONIX_ASSISTANT=1；服务使用 bot start --channels weixin --heartbeat --heartbeat-poll 1s。
[permissions].allow 显式包含 assistant_tasks、assistant_memory。这两个工具如实标记为可写，但只能修改宿主指定的个人状态；模型不能指定数据库路径、收件人、工作区或权限。

当前宿主面向单一微信 owner，通知目标从可信 allowlist 绑定。此版本不支持多用户共享个人记忆。需要多用户时必须先按身份分库和绑定路由，不能直接增加 allowlist 人数后共用现有数据库。

自动分析子进程设置 REASONIX_ASSISTANT_BACKGROUND=1，程序拒绝其修改个人记忆和任务，防止外部邮件/网页内容诱导创建任务。它仍可读当前状态。前台对话是持久修改入口。助手模式会移除通用 remember/forget 写入口，统一使用带时间边界的 assistant_memory。

长短记忆和任务的有效内容各限制约 96 KB；历史任务数量上限 2000、记忆文件 1000；达到上限应整理/导出。不会把全部聊天历史当作永久记忆。上下文中近期执行结果会裁剪，完整结果可通过任务查询检查。请勿把 API Key、登录 token 或密码保存为记忆。

## 运维

- mybot assistant status
- mybot assistant tasks
- mybot assistant memories
- mybot assistant context
- sudo systemctl status mybot-heartbeat
- sudo journalctl -u mybot-heartbeat --since "10 minutes ago"

assistant tasks/memories 是管理状态的只读检查，memories 会包含过期和归档历史；context 仅呈现当前有效状态。旧 heartbeat CLI 使用 heartbeat-cli.json；助手模式的后台任务使用 SQLite，请用 assistant 检查对应状态。既有 JSON 内容不会自动静默迁移；本次升级前已检查旧文件无任务。

备份必须包含完整 assistant 目录；运行时复制 SQLite 请用 SQLite backup API，不能只复制主文件而忽略 WAL。服务切换前保留旧 release 和配置备份。回滚程序指针不会删除新的个人状态数据。

## 测试

scripts/assistant-acceptance.py <二进制> 在临时目录运行真实 Bot 和真实子 Agent，以本地模拟 OpenAI/微信端点验收自然语言入口、工具保存、消息提醒、主动分析、静默、记忆失效和重启去重。它不读取真实密钥、不访问真实微信账号。

完整验证结果与线上验收边界见同目录 TEST_REPORT.md。下载包不含生产密钥、微信 token、个人会话或实际记忆数据；可迁移源码、二进制、配置模板、文档和验证记录。


## 每日会话与主动任务（assistant.4）

- 仅 `REASONIX_ASSISTANT=1` 的微信私聊启用；其他 Reasonix 模式保留原路由。
- 北京时间每天 06:00 分界，标题固定为 `YYYY-MM-DD`。日期归属独立于原生 `CreatedAt`；`SessionID` 由连接、聊天、规范化工作区及日期确定，同日重启保持一致。
- 微信消息在进入队列前标记日期，排队后仍保留原归属。跨 06:00 尚未结束的旧任务继续写入旧会话；新消息使用新日期及独立写入锁。不主动调用模型创建空白日，无需依赖 06:00 定时器准时运行。
- 自动分析先通过网关取得/创建当天会话，读取有限的近期对话参考，再在自己的后台会话中执行；当前有效记忆和待办仍按原机制逐轮加载。实际通知经相同网关串行导入当天会话，后续追问可见。同日会话忙碌时等待当前轮次释放，避免并发写入；到点触发不等于消息平台的硬实时送达保证。静默决策及后台思考不导入日常聊天。
- 固定文字提醒直接发送并记录，不调用模型。后台分析仍消耗正常模型额度；日期选择和命名不消耗模型额度。
- SQLite 的 `assistant_daily_sessions` 保存所属日期和唯一会话映射，`assistant_notifications` 保存发送及导入检查点。成功发送但会话导入失败时，只修复导入；发送结果不确定时不自动重发，不承诺第三方消息平台的绝对 exactly-once。
- `assistant_history` 是宿主绑定的只读工具，支持日期列表、关键字搜索、分页阅读，以及长消息的字符偏移。`date` 指 06:00 至次日 06:00，`calendar_date` 指自然日 00:00 至次日 00:00。先前未按日划分的确定性微信会话保留原样，通过 `legacy` 项回查，不按创建时间假装拆分成每日记录。
- 查询复用原生 v4 的索引和有界窗口；不把全部历史送给模型。索引准备时在同一次工具调用内最多等待 3 秒，避免模型反复调用而持续推进会话。系统提示词、宿主状态快照、隐藏思考及普通工具日志不作为日常对话返回。超过 256 KiB 的单条正文只提供有限预览或略过，并保留截断标记。
- 历史内容是参考资料，不自动成为当前指令或重新激活过期记忆。后台任务历史同时保留执行会话 ID 与日常会话 ID。
- 日常模式的 `/new`、`/reset`、`/attach` 返回日期管理说明，避免绕过“一天一条”规则；按日期回查使用普通语言或只读工具。其他模式不变。
- 集成 `bot start --heartbeat` 和启用了原有鉴权回环控制接口的独立 Heartbeat worker 都使用相同入口；不新增公网监听。

验收脚本：`python3 scripts/daily-conversation-acceptance.py bin/reasonix-daily`，仅使用临时状态和本机模拟 OpenAI/微信接口，不向真实微信发送消息。生产部署脚本为 `scripts/deploy-daily-conversations.py`。
