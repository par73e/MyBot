# MyBot / Friday

基于 Reasonix `v1.39.10` 的云端私人助理，通过微信对话管理待办、定时提醒及长期/短期记忆，由常驻 Heartbeat 调度主动任务。

当前服务版本为 `v1.39.10-assistant.10-output-closeout`，模型配置为 `qwen3.8-flash`，提供 Low、Medium、XHigh 档位。日常上下文不自动加载部署环境与工作目录，技术工具通过按需入口使用。微信日常会话每天北京时间 06:00 切换、按日期命名；主动通知进入同一天会话，历史可以按需查询。和风天气查询与每日晨报已接入，支持按问题查询天气、空气质量和官方预警；新闻和日历尚未接入。统一邮件工具和 Heartbeat 分钟级检查已部署，支持通过 Agent 修改运行时段及暂停/恢复；当前邮件每周重复：周一/三/四 10:00、周二/五 08:00、周六/日 11:00 开启，每天 24:00 关闭（北京时间）。天气晨报按相同起床时间每日播报一次，工作日学校、周末家仅作为地点候选，Agent结合长期／短期记忆及本次问题决定实际查询地点；未来安排写入后即进入上下文，按行程日期使用。邮件检查与天气晨报独立控制。微信已启用程序级最终答复过滤，详见[验证说明](reasonix-assistant-v1.39.10/verification/FINAL_ONLY.md)。

## 当前版本

当前源码在 [`reasonix-assistant-v1.39.10/`](reasonix-assistant-v1.39.10/)。

- [私人助理使用说明与架构](reasonix-assistant-v1.39.10/ASSISTANT.md)
- [Friday 系统提示词](reasonix-assistant-v1.39.10/deploy/tencent/friday.md)
- [配置模板](reasonix-assistant-v1.39.10/deploy/tencent/assistant.example.toml)
- [核心模块测试报告](reasonix-assistant-v1.39.10/TEST_REPORT.md)
- [Friday 身份更新及验证](reasonix-assistant-v1.39.10/verification/FRIDAY_PERSONA_UPDATE.md)
- [Friday 日常上下文调整及验证](reasonix-assistant-v1.39.10/verification/FRIDAY_CONTEXT_UPDATE.md)
- [每日会话与主动任务验收](reasonix-assistant-v1.39.10/verification/DAILY_CONVERSATIONS.md)
- [天气选址与未来记忆调整](reasonix-assistant-v1.39.10/verification/WEATHER_CONTEXT.md)
- [天气查询、晨报与验收说明](reasonix-assistant-v1.39.10/verification/WEATHER.md)
- [邮件能力、验收与上线说明](reasonix-assistant-v1.39.10/verification/MAIL.md)
- [源码来源](reasonix-assistant-v1.39.10/SOURCE_PROVENANCE.md)

使用 Go `1.26.10` 在源码目录构建：

```sh
cd reasonix-assistant-v1.39.10
go build -ldflags '-X main.version=v1.39.10-assistant.10-output-closeout' -o bin/reasonix ./cmd/reasonix
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
