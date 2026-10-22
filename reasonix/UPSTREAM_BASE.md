# 上游来源与本地改造

- 上游仓库：https://github.com/esengine/DeepSeek-Reasonix
- 选用版本：`v1.39.10`，Reasonix 1.x 稳定发布。
- 对应提交：`195fc08132d253270de7d88afc8d395e3f96083e`。
- 官方归档：https://codeload.github.com/esengine/DeepSeek-Reasonix/tar.gz/refs/tags/v1.39.10
- 归档 SHA-256：`6993258fe438e64cc32932e00bea179c0949af3a04475d148642bdeabd37333a`。
- 本地版本标识：`v1.39.10-heartbeat.1`。
- 编译器：Go `1.26.10`，`CGO_ENABLED=0`。

Git 克隆连接超时，因此下载并解压官方 tag 归档；此目录没有 `.git` 元数据。
完整改动另存于工作区根目录的 `heartbeat-v1.39.10.patch`，可应用到该 tag。
未修改 `go.mod`、`go.sum` 或桌面端源码。

新增内容是独立的 `internal/heartbeat` 调度模块，以及命令行／Bot 接入。
时间规则移植自本版本的 `desktop/heartbeat_schedule.go`。
使用方法见 [命令行 Heartbeat](docs/HEADLESS_HEARTBEAT.zh-CN.md)。

构建命令（两个目标分别执行）：

```bash
CGO_ENABLED=0 go build \
  -ldflags '-X main.version=v1.39.10-heartbeat.1 -X main.gitCommit=195fc08132d2' \
  -o bin/reasonix ./cmd/reasonix
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags '-X main.version=v1.39.10-heartbeat.1 -X main.gitCommit=195fc08132d2' \
  -o bin/reasonix-linux-amd64 ./cmd/reasonix
```
