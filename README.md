# MyBot / Friday

基于 Reasonix `v1.39.10` 的云端私人助理，通过微信对话管理待办、定时提醒及长期/短期记忆，由常驻 Heartbeat 调度主动任务。

当前服务版本为 `v1.39.10-assistant.2`，模型配置为 `qwen3.8-flash`，提供 Low、Medium、XHigh 档位。邮箱、天气、新闻和日历等业务数据源尚未接入。

## 当前版本

当前源码在 [`reasonix-assistant-v1.39.10/`](reasonix-assistant-v1.39.10/)。

- [私人助理使用说明与架构](reasonix-assistant-v1.39.10/ASSISTANT.md)
- [Friday 系统提示词](reasonix-assistant-v1.39.10/deploy/tencent/friday.md)
- [配置模板](reasonix-assistant-v1.39.10/deploy/tencent/assistant.example.toml)
- [核心模块测试报告](reasonix-assistant-v1.39.10/TEST_REPORT.md)
- [Friday 身份更新及验证](reasonix-assistant-v1.39.10/verification/FRIDAY_PERSONA_UPDATE.md)
- [源码来源](reasonix-assistant-v1.39.10/SOURCE_PROVENANCE.md)

使用 Go `1.26.10` 在源码目录构建：

```sh
cd reasonix-assistant-v1.39.10
go build -ldflags '-X main.version=v1.39.10-assistant.2' -o bin/reasonix ./cmd/reasonix
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
