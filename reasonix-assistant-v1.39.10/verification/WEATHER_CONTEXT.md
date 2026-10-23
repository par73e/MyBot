# 天气选址与未来记忆

版本：`v1.39.10-assistant.8-weather-context`。沿用已部署的天气接口、晨报时间、邮箱设置及个人记忆文件，不新增旅行地点覆盖配置，不迁移或伪造用户行程。

## 新流程

`宿主提供默认地点候选及当前/未来记忆 → Agent综合问题与查询日期决定地点 → 工具校验并按该地点调用天气API → Agent分析真实结果 → 回复或晨报推送`。

每轮个人上下文增加 `weather_defaults`，包含默认星期规则、地点名称/ID、今天候选的自然日与星期；不包含私有地址坐标。`assistant_weather status`可读取其他目标日期的候选，不调用天气API。

Agent显式提供配置地点ID或坐标+名称，说明 `location_reason` 与 `location_source`（schedule、memory、explicit_question）。晨报继续限定今天全天及完整组件，但允许Agent选择旅行目的地；不会在取数后再把默认城市的数据重新贴上目的地标签。工具返回实际地点、来源和理由。

程序不解析自然语言记忆、不自行判断行程地点，也不静默覆盖Agent所选地点。仅当Agent声明使用schedule时，校验它确实使用了该目标自然日的宿主候选；声明memory或explicit_question时由Agent决定。这防止凌晨将06:00换日的会话日期误当自然日而选错默认地点。

## 未来记忆立即可见

`starts_at`控制事实/偏好的适用时间，不再作为可见性门槛。未归档且未过期的所有条目写入后即可被读取：当前适用内容在 `active_memories`，未来内容在 `known_future_memories`，二者均保留完整正文和时间；默认memory list同样包含未来条目。

例如一个月后旅行的记忆今天即可回答“我下个月有什么安排”；明天起的行程今天即可用于“明天天气如何”。Agent按正文行程及时间字段匹配问题日期，不把未来事件说成现在已发生。过期或归档后正文退出当前上下文，保留状态与可显式查阅的历史；不会从旧对话恢复失效行程。可见记忆总正文仍限制为96KB，未来条目同样计入。

## 验证

- `weather_context_test.go`覆盖未来一个月的记忆立即可见、到达适用时间、精确过期、归档、长期记忆保留、默认候选不取天气、按Agent指定坐标取完整晨报、明确问学校时不被旅行记忆替换，以及声明schedule时的日期校验。
- `weather-context-acceptance.py`使用真实Bot/子Agent/工具/SQLite，模拟模型、天气和微信；覆盖未来旅行、今天默认、当前旅行、明确学校、旅行晨报、过期恢复、未来一个月安排可见及重启去重。没有向真实微信发送测试消息。
- `weather-context-model-probe.py`在服务器隔离目录使用真实云端模型和真实天气API，保存私有工具调用证据，核验最终实际查询地点，而非只检查回复是否提到城市。测试行程仅写入隔离目录，不进入生产记忆。
- assistant、boot、cli、heartbeat、bot相关测试通过；control的个人助理/回合上下文相关测试通过；assistant/heartbeat竞态检查、golangci-lint及repolint通过，基线未增加。
- 完整control测试发现一个本机环境失败：`TestServeForkStartsNoBroaderThanInheritedReadOnly`因macOS宿主不允许sandbox_apply，无法启用只读沙箱。用v7源码覆盖本轮改动后同一测试仍以同一原因失败；这是已复现的环境限制，未绕过权限、修改该用例或声称全套测试全绿。

验收结果、构建摘要与实际部署状态分别见 `weather-context-acceptance.log`、`weather-context-model-probe.json`、`weather-context-build.json`、`weather-context-deployment.json` 和 `weather-context-postcheck.json`。源码同步到当前发布目录并逐项核对SHA256；运行进程的二进制SHA必须匹配本地Linux发布包。
