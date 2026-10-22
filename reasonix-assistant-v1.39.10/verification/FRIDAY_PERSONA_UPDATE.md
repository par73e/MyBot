# Friday 身份更新（2026-10-22）

基础版本：v1.39.10-assistant.1；本次服务器程序版本：v1.39.10-assistant.2。

更新范围：

- `deploy/tencent/friday.md`：Friday 私人助理身份、交流方式、记忆和待办使用、能力边界。
- `internal/boot/prompt_policy.go`：仅助手模式使用通用助理执行规范，保留共同的决策和语言规则。
- `internal/control/assistant_prompt.go`、`session_binding.go`：重开助手会话时刷新模型上下文中的基础身份，不改写可见聊天记录。
- `scripts/check-friday-persona.py`：隔离临时 HOME，通过真实上游请求验证旧会话在配置更新后使用新身份，并验证记忆和待办工具。

原始 `reasonix-assistant-v1.39.10.tar.gz` 是此前版本的归档，保持不变。本目录下的补丁源码、提示词和后续验证记录对应本次更新。
