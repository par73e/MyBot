# Friday 每日会话与主动任务验收

日期：2026-10-23（Asia/Shanghai）。版本：`v1.39.10-assistant.4`，基于 `v1.39.10`；保留 assistant.3 的身份和上下文调整。

## 最终行为

微信私聊采用北京时间 06:00 分界，原生会话标题为 `YYYY-MM-DD`。聊天身份、规范化工作区和所属日期决定稳定的会话 ID；持久化索引记录起止时间，重启不另建一份。当日第一个触发者可以是用户消息、固定提醒或后台分析。

后台分析在单独执行会话中运行，先取得当天会话的有限参考上下文；实际通知经网关的同日写入锁进入日常会话，下次追问可见。静默结果不写成聊天消息。固定提醒不调用模型。后台执行 ID 与日常会话 ID 分别保留。

原有未按日划分的微信会话保留，历史工具能只读回查，不将创建日期冒充成全部聊天日期。自然日查询与 06:00 会话日期分开解释。当前有效长期记忆、短期记忆、待办仍独立加载；历史查询不能复活过期记忆。

## 验证结果

- Linux 完整回归：`assistant`、`bot`、`control`、`heartbeat`、`cli`、`boot` 六个相关包通过，见 [日志](daily-linux-tests.log)。后续新增 Heartbeat 入场时间/双会话 ID 回归也通过。
- macOS 新增功能测试通过；相关助理测试启用 `-race` 后五个包通过，见 [并发检测](daily-race.log)。macOS 完整测试中遇到的 sandbox-exec 外层权限限制，已将该测试单独移到外层沙箱之外复核通过；Linux 完整回归无此限制。
- golangci-lint：0 issues；仓库 repolint：clean，保留原有 1152 条基线，没有提高基线预算。见 [Go lint](daily-lint.log)、[仓库检查](daily-repolint.log)。
- 实际二进制端到端测试在 macOS 与 Linux 均通过。临时状态、本机模拟 OpenAI/微信接口；每次 6 次模型接口请求、6 条模拟微信消息、1 次原生历史工具调用、2 条已完成的通知 outbox 记录。没有向真实微信发送验收消息。见 [macOS](daily-acceptance.json)、[Linux](daily-linux-acceptance.json)。
- 覆盖：主动任务先于首条聊天创建日期会话、用户追问衔接、重启恢复、原生标题持久化、静默分析、固定提醒、历史查询、05:59:59/06:00 边界、UTC 时间换算、同日并发创建、旧/新日期锁隔离、排队日期保留、旧记录凌晨归属、长消息续读、用户隔离、发送成功后仅修复会话导入、模糊发送拒绝盲目重试、回环接口鉴权。

## 发布与同步

服务器运行路径：`/opt/mybot/releases/v1.39.10-assistant.4/reasonix`；`/opt/mybot/current` 指向该版本。`mybot-heartbeat.service` 为 active/running，微信 getupdates 已就绪，发布后检查 NRestarts=0。

发布备份：`/var/lib/mybot/backups/daily-conversations-20261023T041243Z`，包含上一版本指针及服务器本机的 SQLite 一致性备份。发布脚本校验配置、微信凭据、Friday 提示词和个人记忆 MD 未改变。没有把生产密钥、聊天或个人记忆下载到本地。

本地源码：`reasonix-assistant-v1.39.10/`；云端源码：`/home/ubuntu/Project/MyBot/reasonix/`。本地原生二进制 `bin/reasonix-daily`，Linux 二进制 `bin/reasonix-linux-amd64-daily`。二进制校验值见 [构建记录](daily-build.json)，发布结果见 [部署记录](daily-deployment.json)。历史版本二进制与归档保留。生产配置和状态保留在服务器，不通过源码同步覆盖。

## 边界

- 同日会话正在处理消息时，主动任务需要等待该轮释放；调度及消息平台不提供硬实时到达保证。
- 历史按需分页读取；超过 256 KiB 的单条正文只提供有限预览或略过。工具不返回系统提示词、宿主上下文、隐藏思考或一般工具日志。索引准备时先在同一次工具调用内短暂等待，避免反复唤起模型。
- 平台发送与本地数据库无法形成一个原子事务；遇到发送结果不确定时保留记录供检查，不自动重发。已确认发送但会话导入失败时只修复导入。
- 旧会话不自动拆分，不产生每日摘要，不清理历史记录。邮箱、天气、新闻等数据源不在本次接入范围。
