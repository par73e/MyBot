# 命令行 Heartbeat（本地改造版）

基于 Reasonix 1.x 稳定版 `v1.39.10`，上游提交
`195fc08132d253270de7d88afc8d395e3f96083e`。桌面端无需启动。
本地构建版本标记为 `v1.39.10-heartbeat.1`。

## 已实现的范围

- 移植桌面的间隔、Cron、每日／每周等时间规则，支持显式 IANA 时区。
- 增加一次性时间、结束时间以及固定消息任务。
- 提供 CLI 创建、查看、启停、删除、手动执行和常驻调度入口。
- `bot start --heartbeat` 将调度器与现有 Bot 网关同时启动。
- 间隔任务支持工作时间窗；所有后台 Agent 默认使用 `read-only`。
- 默认每 30 秒检查，最小检查间隔 1 秒；空闲检查不调用模型。
- 固定消息直接发送，不调用模型；分析任务通过同一二进制的 `run` 执行。
- 默认每次使用新会话；`--reuse-session` 只恢复该任务自己的会话。
- JSON 原子保存，跨进程文件锁，同一时刻串行执行任务。

本次没有安装邮箱、天气、新闻等业务工具，也没有增加专用的自然语言
任务管理工具。当前通过 CLI 或任务文件管理；这些是后续可接入的功能。
原有记忆系统不在本次改造范围中。

## 本地构建与隔离

```bash
go build -o bin/reasonix ./cmd/reasonix
```

测试版请始终指定独立的 `REASONIX_HOME`。这个变量使用 Reasonix 原有的
隔离机制，覆盖配置、凭据、会话等数据目录；不要直接复用已安装版本的目录。
所有示例中的路径和时间都需要替换为自己的值。

```bash
export REASONIX_HOME=/tmp/mybot-heartbeat-dev
./bin/reasonix heartbeat help
```

Agent 任务使用这个独立目录里的模型配置。按照上游配置模型和凭据；
模型密钥需使用 Reasonix 自己的凭据存储，单独导出同名进程环境变量并不
等同于已配置模型凭据。

## 创建与管理

每个工作日 08:00 执行任务：

```bash
./bin/reasonix heartbeat add \
  --title '每日早报' --prompt '读取已接入的信息源，整理今天的安排。' \
  --interval '0 8 * * 1-5' --timezone Asia/Shanghai --dir /absolute/workspace
```

一次性提醒（将时间改为未来的时刻）：

```bash
./bin/reasonix heartbeat add \
  --title '提交报告' --prompt 'Sir，记得提交报告。' --kind message \
  --at '2026-10-23T17:00:00+08:00' --timezone Asia/Shanghai \
  --dir /absolute/workspace
```

默认只将结果保存到任务历史，不会选择收件人。需要发送微信时，创建时追加
`--connection weixin-weixin --chat YOUR_CHAT_ID`。连接 ID 和会话 ID 必须来自
自己的 Bot 配置；不会向所有已绑定会话广播。

```bash
./bin/reasonix heartbeat list
./bin/reasonix heartbeat disable --id TASK_ID
./bin/reasonix heartbeat enable --id TASK_ID
./bin/reasonix heartbeat remove --id TASK_ID
./bin/reasonix heartbeat run --id TASK_ID
```

`run` 是真实执行，会忽略原定时间和启用状态，但仍遵守结束时间。
如任务有待发送结果，`run` 优先重试该结果，不重新调用模型。
已停用任务的待发送结果仍保持暂停，需先 `enable` 才会补发。
任务定义可直接编辑 JSON；每次检查重新读取，编辑期间也需避免覆盖运行状态。

`--interval '15m' --window-start 08:00 --window-end 23:00` 表示只在该时间窗内
按间隔检查。结束时间不包含在窗口中。Cron 使用 5 字段，日期和星期同时
受限时按标准 Cron 的 OR 语义处理。时间窗只适用于普通间隔规则。

`--ends-at '2026-10-30T00:00:00+08:00'` 设置排他的失效边界；超过边界不再
启动或补发通知。每天不同时间可建立多条按星期匹配的任务。

## 常驻与 Bot 接入

独立调度进程：

```bash
./bin/reasonix heartbeat start --poll 30s
```

随 Bot 网关启动（推荐用于服务器）：

```bash
./bin/reasonix bot start --channels weixin --dir /absolute/workspace --heartbeat
```

Bot 集成模式直接使用同一进程里的网关发送，无需打开 HTTP 控制接口。
独立调度进程发送时，要求已有 Bot 网关开启 `[bot.control]`，并有可用的
`token_env`。仅接受 loopback 地址，拒绝重定向。`heartbeat tick` 仅检查一次，
可以交给系统计划任务；`start` 则自行常驻。不要同时配置多套自动启动方式。

任务保存在 `REASONIX_HOME/heartbeat-cli.json`（默认目录使用 Reasonix 自身
用户目录），与桌面端的 `heartbeat-tasks.json` 分开。`--file` 或 Bot 的
`--heartbeat-file` 可以显式指定另一份文件，两个入口要使用同一个路径。

## 运行状态与失败处理

`list` 的 `history` 最多保留 20 次：

- `running`：已持久化执行占用，再启动 Agent。
- `completed`：执行成功，无需通知，或只在本地保存结果。
- `failed`：执行失败；一次性任务停止，周期任务等待下一个周期。
- `delivery_pending`：已有结果，等待发送或明确失败后的重试，不重新运行模型。
- `delivered`：发送接口确认成功；一次性任务自动停用。
- `interrupted`：上次进程在执行过程中退出；不会自动重放可能有副作用的任务。
- `delivery_uncertain`：部分发送或 HTTP 结果不明确；停止自动重试以减少重复通知。
- `delivery_expired`：待发送结果超过任务结束时间，不再发送。

对中断的一次性任务，检查后可用 `run --id` 明确重试。周期任务不会补跑
每一个错过的周期。Cron 在匹配分钟运行；桌面的每日／每周规则会补最近一轮，
一次性任务在下次上线时执行。对不应迟到补发的任务，设置 `--ends-at`。

模型最终答案恰好为 `HEARTBEAT_OK` 时静默完成；可在监控任务中约定这个输出。
Agent 执行限制为 10 分钟，取消时清理子进程组；标准输出和错误各限制为
1 MiB。网络和外部平台无法提供严格的“恰好一次”交付保证。

30 秒是检查间隔，并非消息送达承诺。Agent 运行、串行排队、网络和微信平台
都会增加延迟。需要提前准备的早报应在目标时间之前安排分析。

## 验证

```bash
go test ./...
go test -race ./internal/heartbeat ./internal/cli -run 'TestHeartbeat|TestBotHeartbeat|TestOneShot|TestDelivery|TestWorkerLease'
make lint
python3 scripts/test-headless-heartbeat.py --binary bin/reasonix
```

端到端脚本启动本地模拟模型和 Bot HTTP 服务，使用临时配置／工作区，验证真实
二进制的定时启动、Agent JSON 结果、发送重试、重启去重和零模型固定提醒。
不会访问真实模型额度、邮箱或微信账号。
