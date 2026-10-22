# 模型与微信配置验收

2026-10-22（北京时间），在腾讯服务器配置完成。

后续已将默认思考强度设为 Low 并保留 Medium、XHigh，见 [档位配置](REASONING_PRESETS.md)。
下列“未覆盖思考参数”是最初接入时的快照。

- 兼容协议：OpenAI Chat Completions。
- API 地址：`https://maas.qianwenaiapi.com/compatible-mode/v1`。
- 实际拉取模型：178 个，无重复 ID，包含 `qwen3.8-flash`。
- 默认模型路由：`ali/qwen3.8-flash`，Heartbeat 与微信聊天共用。
- 未覆盖模型思考开关、思考强度或其他额外请求参数。
- API Key 通过 SSH 加密通道传输，存于服务器 `.env`，归属 `mybot:mybot`、权限 0600。
  报告与配置示例不包含 Key 内容。
- 使用实际 Reasonix 二进制调用真实模型，成功返回 `OK`，本次总耗时 2.22 秒。
  这是一条短测试的测量结果，不代表其他消息的响应时间保证。
- 微信登录凭据已保存；访问名单仅包含扫码账号，未知账号配对入口关闭。
- 工具权限当前为 `read-only`，HTTP 控制接口未启用。
- systemd 服务已切换为微信网关与 Heartbeat 同进程运行，开机自启和异常恢复设置保留。
- 日志显示 18:05:31 收到一条微信入站消息，18:05:36 完成该轮处理，未见发送失败日志。
  手机端最终显示情况仍以用户实际接收为准。
- 原有 AURORA 与 Nginx 保持运行。

启动覆盖配置为 `10-weixin.conf`。日常查看状态与日志的命令见
[服务器使用说明](SERVER_GUIDE.zh-CN.md)。
