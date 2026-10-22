# 命令行 Heartbeat 测试报告

验证日期：2026-10-22，macOS arm64。源码基于官方 `v1.39.10`，
本地构建标记 `v1.39.10-heartbeat.1`，编译器 Go `1.26.10`。

本报告记录本地交付时的状态。后续已完成腾讯服务器部署和 Linux 验证，
见 [服务器部署验收](deploy/tencent/DEPLOYMENT_REPORT.md)。

## 验证结果

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| 根 Go 模块 `go test ./...` | 通过，158 个包测试通过，9 个包无测试，零失败 | [日志](verification/go-test-all.log) |
| `go test -race ./internal/heartbeat ./internal/cli` | 通过 | [日志](verification/go-test-race.log) |
| `make lint` | 通过；golangci-lint 零问题，repolint 未增加基线问题 | [日志](verification/lint.log) |
| 实际二进制端到端测试 | 通过 | [日志](verification/end-to-end.log) |
| macOS arm64 构建 | 通过，可运行，版本输出正确 | `reasonix/bin/reasonix` |
| Linux amd64 构建 | 通过，静态 ELF；尚未在 Linux 运行 | `reasonix/bin/reasonix-linux-amd64` |
| 官方 tag 上应用补丁并逐文件比较 | 通过，重现全部 22 个改动／新增文件 | `heartbeat-v1.39.10.patch` |
| CLI 帮助及 Bot Heartbeat 参数 | 通过 | `heartbeat --help`、`bot start --help` |

全量 Go 测试包含上游 macOS 沙箱集成测试，需要在外层运行沙箱之外执行。
最终全量回归退出码为 0。桌面端独立 Go 模块与前端未修改，未运行桌面 UI 测试。

## 关键行为

端到端测试启动真实编译后的 CLI，并使用临时配置、工作区、本地模拟模型及
Bot 消息接口。一次性任务按时唤起 Agent，只调用模型一次；第一次发送返回
HTTP 503 后，第二次成功，发送重试未重新调用模型。重启后执行 `tick` 没有
重复运行已完成任务。固定消息任务的模型调用次数为零。

新增自动化测试还覆盖：时区／Cron／到期边界、普通间隔的时间窗、跨进程互斥、
并发修改保留、删除中的任务不复活、中断任务不自动重放、部分发送停止自动重试、
Agent 失败不作为成功通知发送、排队任务开始前重新检查到期时间，以及工作目录
消失不阻塞其他任务。Bot 集成用模拟网关验证了启停生命周期和定向消息发送。

这些验证不代表已接通真实 Qwen API、微信、邮箱或天气服务。未修改已安装
Reasonix 的配置，未部署到腾讯服务器。尚未加入对话自动创建任务工具或短期
记忆管理；本次任务创建入口是 CLI，记忆系统保持上游实现。

## 交付文件校验

```text
6bcca1f891e89d9a2203884be06d70bebbdb29213e91cb20d71cae1e43a60233  reasonix/bin/reasonix
676904f2fd4a632756074d523de36c626d52c04ab54c00ca148f7fb1f13e3372  reasonix/bin/reasonix-linux-amd64
c9f398655ad486f9c2015a4847091f6f970626d2964e9a6cc0373be2cd351c66  heartbeat-v1.39.10.patch
```

详细操作与时间规则见 [使用说明](reasonix/docs/HEADLESS_HEARTBEAT.zh-CN.md)，
上游归档及构建参数见 [来源记录](reasonix/UPSTREAM_BASE.md)。
