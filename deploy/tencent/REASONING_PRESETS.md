# Qwen3.8 Flash 思考档位

2026-10-22 配置完成，最初默认 Low；随后按用户要求将全局与微信默认切换到 Medium。
Low、Medium、XHigh 三个预设均保留。

阿里云文档给出的原生三档是 `low`、`medium`、`xhigh`。
`high` 是映射到 `xhigh` 的兼容别名，配置直接使用模型原生名称。
参考：<https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions/>。

## 微信切换

| 档位 | 发给机器人的命令 |
| --- | --- |
| Low | `/model ali/qwen3.8-flash` |
| Medium（当前默认） | `/model ali-medium/qwen3.8-flash` |
| XHigh（更高） | `/model ali-xhigh/qwen3.8-flash` |

三项使用同一个真实模型、API 地址和私有 Key，仅请求中的 `reasoning_effort`
取值不同。当前微信网关支持 `/model` 管理命令，没有单独的 `/effort` 处理器，
因此使用 provider 配置预设进行切换。切换只影响当前微信会话。
不要依靠自然语言“切换为高思考”作为参数已改变的证据。

Heartbeat 使用全局默认 `ali-medium/qwen3.8-flash`，即 Medium；如需特定任务使用其他档位，
创建任务时通过 `--model ali/qwen3.8-flash` 或 XHigh 预设指定。

## 配置

全局默认和 Bot 默认现为 `ali-medium/qwen3.8-flash`。三个 provider 都声明：

```toml
reasoning_protocol = "openai"
supported_efforts = ["low", "medium", "xhigh"]
```

`ali` 的 `effort` 和 `default_effort` 为 `low`；其余两个分别为 `medium`、`xhigh`。
未设置 `thinking_budget`，以免与 `reasoning_effort` 冲突。
未关闭思考；Low 表示低思考力度，仍由模型执行推理。

## 验证

`verify-reasoning.py` 在服务器的私有临时配置中复制三个实际预设，用本地转发器
观察真实 Reasonix 二进制发出的请求，只记录模型和思考参数，再转发到配置的上游。
没有记录密钥、请求头或用户聊天内容。

| 预设 | 实际 HTTP 字段 | 上游调用 | 本次短测试耗时 |
| --- | --- | --- | --- |
| ali | `reasoning_effort=low` | 成功 | 1.52 秒 |
| ali-medium | `reasoning_effort=medium` | 成功 | 1.64 秒 |
| ali-xhigh | `reasoning_effort=xhigh` | 成功 | 1.50 秒 |

这验证了客户端实际发送参数且上游接受请求；短回答的耗时不用于推断各档智力、
复杂任务速度或上游内部推理预算的严格执行情况。
配置完成后，在无活跃聊天回合时重启服务，使新配置生效。
原配置备份保存在服务器私有目录的 `config.before-effort-low-20261022.toml`。
