# 腾讯服务器运行说明

部署于 2026-10-22（北京时间），通过 SSH 别名 `tencent` 访问。
当前版本：`v1.39.10-heartbeat.1`。

## 当前状态

微信网关和 Heartbeat 已同进程运行并启用开机启动，每 30 秒检查一次定时任务。
异常退出后 5 秒自动重启。默认模型为 `ali-medium/qwen3.8-flash`，思考档位现为 Medium。
微信已扫码绑定，访问名单仅包含扫码账号。当前没有配置定时业务任务。
模型与微信配置验收见 [配置报告](MODEL_WEIXIN_SETUP.md)。
Medium、XHigh 切换命令见 [思考档位说明](REASONING_PRESETS.md)。

## 日常操作

先在 Mac 终端登录：

```bash
ssh tencent
```

在服务器上执行：

```bash
mybot --version
mybot heartbeat list
systemctl status mybot-heartbeat --no-pager
sudo journalctl -u mybot-heartbeat -n 50 --no-pager
```

手动重启、停止或启动：

```bash
sudo systemctl restart mybot-heartbeat
sudo systemctl stop mybot-heartbeat
sudo systemctl start mybot-heartbeat
```

创建任务时，工作目录请明确指定 `--dir /var/lib/mybot/workspace`；
任务文件和配置由独立的 `mybot` 系统账号管理，`mybot` 命令会自动切换账号和数据目录。
实际操作命令需要在服务器执行；本地 Mac 的同名文件是另一份安装。

## 安装位置

| 内容 | 服务器路径 |
| --- | --- |
| 当前可执行文件 | `/opt/mybot/current/reasonix` |
| 固定版本目录 | `/opt/mybot/releases/v1.39.10-heartbeat.1` |
| 配置 | `/var/lib/mybot/reasonix/config.toml` |
| Heartbeat 任务 | `/var/lib/mybot/reasonix/heartbeat-cli.json` |
| Agent 工作目录 | `/var/lib/mybot/workspace` |
| 便捷命令 | `/usr/local/bin/mybot` |
| systemd 单元 | `/etc/systemd/system/mybot-heartbeat.service` |
| 微信启动覆盖配置 | `/etc/systemd/system/mybot-heartbeat.service.d/10-weixin.conf` |
| 模型凭据文件 | `/var/lib/mybot/reasonix/.env`（私有文件，不要输出内容） |

服务使用独立账号，内存上限 1 GiB，CPU 上限相当于一核；这是运行资源限制，
并非全量业务所需内存的测量结论。数据目录权限为 0700，配置／任务文件为 0600。
本次服务没有新增监听端口。

## Shell 沙箱依赖

已安装 Ubuntu 软件包 `bubblewrap 0.9.0-1ubuntu0.3`。
Ubuntu 24.04 的用户命名空间限制使默认 bwrap 无法启动；因此从对应 Ubuntu
`apparmor-profiles 4.0.1really4.0.1-0ubuntu0.24.04.9` 软件包提取并启用了
`bwrap-userns-restrict` 单独配置，位置是 `/etc/apparmor.d/bwrap-userns-restrict`。
没有安装整个 profiles 包，也没有关闭系统级用户命名空间限制；该内核设置保持为 1。

已在服务使用的权限限制下验证：bwrap 可以启动，只读文件系统写入被拒绝，
网络命名空间无法连接宿主机 SSH 监听端口。
Ubuntu 对这一机制的说明：<https://documentation.ubuntu.com/release-notes/24.04/#unprivileged-user-namespace-restrictions>。

## 微信连接

已使用 `bot start --channels weixin --heartbeat` 启动。systemd 服务名仍为
`mybot-heartbeat`，通过 `10-weixin.conf` 覆盖启动命令；不需要再运行第二个网关
或独立调度进程。

微信凭据由 Reasonix 保存在私有用户目录中。首次实际调用已收到微信入站消息，
并完成了一轮模型回复。模型 Key 与微信连接凭据分别存储，更新模型 Key 无需
重新扫码。只有连接凭据失效等登录问题才需要重新处理扫码。
