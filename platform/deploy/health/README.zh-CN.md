# Friday 只读服务器健康

2026-11-05 已经用户授权上线安全/访问汇总与修改后的健康采集器。平台镜像 asrobot:health-20261105，部署记录位于服务器 /opt/asrobot/releases/health-20261105/。健康检查不执行任意请求命令，不得作为构建步骤自动安装或重启服务。健康检查不读取邮件、天气、凭据或个人记忆，也不执行重启或写操作。

## 原基础方案（历史边界）

- 采集器由独立的无登录 `asrobot-health` 用户运行，仅读 `/proc/meminfo`、根文件系统空间、系统负载、固定 Friday systemd 单元以及 `https://asrobot.par73e.cn/asrobot/api/health`。实例服务 active 只能证明 supervisor 单元运行，不能证明 bot 或微信消息送达。
- AS Robot 容器状态因不授予 Docker socket 权限而明确返回 unknown。网站 HTTP 200 只代表入口响应，不等于容器及所有业务健康。
- 服务固定读取本机 `/run/asrobot-health/friday.sock`；连接者须是配置的唯一实例 UID，既受 socket 目录/group 约束，也由 Linux SO_PEERCRED 验证。请求只接受 `status`。根本不提供 URL、命令或任意路径参数。
- 不配置未经验证的资源健康阈值；采样成功仅表示已读取。采集时失败或超过 60 秒显示 unknown。

## 核对和安装步骤（上线前仅供审查，不执行）

1. 只读核对线上 `asrobot-instance-<实例ID>.service`、其账户/组、AS Robot 网站和现有实例状态。重新核对所有路径、哈希、Linux 架构与待发布构建，不依赖历史时间点。确认使用当前工作区源码构建，保留此次未提交天气预警功能且不覆盖其他改动。
2. 在隔离 Linux 环境先创建等价的普通测试用户和虚拟实例，验证 socket activation、SO_PEERCRED、目标单元访问、TLS、超时以及异用户连接被拒。为发布目录准备与运行用户隔离、root 拥有且不可被应用用户修改的候选程序和服务文件。构建 Friday、bridge 和平台镜像；将二进制/镜像哈希、现有服务与配置快照和非敏感检查结果记录在发布清单中。实际线上文件不在本步骤替换。
3. 上线经授权后，准备无登录 `asrobot-health` 账户，安装 root 拥有、0755 的采集程序。将 socket 模板中的 `FRIDAY_INSTANCE_GROUP` 设置为核验过的实例主组，将 service 模板中的 `FRIDAY_INSTANCE_ID` 设置为该 32 位实例 ID。创建 `/run/asrobot-health` 为 root:实例主组、0750，使用 root 拥有的 tmpfiles 配置保持重启后权限；socket 模板 mode 0660。此目录不得由实例用户写入。安装并验证模板文件的所有权、权限及 systemd 语法，再按变更流程启动 socket，检查已授权 UID 成功、其他 UID 被拒。
4. 将新应用、bridge 和 Friday 二进制以既有平台发布方式分阶段上线。先确认备份和清单，保留现有邮件/天气/微信配置、状态及实例数据；监视服务恢复，并将**正在运行的进程实际执行文件**与候选摘要核对。网页和微信分别执行只读检查；没有授权时不发送微信测试消息。不得把 API 接受、进程运行或 HTTP 200 说成微信实际送达。
5. 上线后核验个人记忆列表、现有业务、天气预警和网站访问；不要为验收修改生产个人记忆。首次健康检查应有采集时间，AS Robot 容器显示未知。仅在隔离实例验证修改、归档和冲突。

## 回退

保留上线前程序、bridge 和网站镜像的版本及哈希。必要时由授权维护者恢复旧程序与镜像，停用新增 socket/service，并只移除新增采集器文件；保留现有实例数据、会话、记忆及上线后新增数据，不用旧数据库或目录快照覆盖新数据。回退后验证旧程序进程摘要、实例、网站及原有配置。处理远端服务的停止、重启、删除均需当时明确授权。


## 2026-11-05 已上线改进

- 删除固定的 Docker unknown 项和长权限说明；网页及微信摘要只展示支持的采集项。
- server_activity_snapshot.py 由root oneshot/timer每5分钟运行，仅调用固定fail2ban-client status及读取固定Fail2ban/Nginx日志，导出汇总。助手仍只有健康socket访问权，采集器不加入adm/docker组。
- /run/asrobot-activity/snapshot.json只含计数、覆盖范围和时间；10分钟后判过期。日志只读当前及前一个未压缩轮转文件，每文件限制32MB；截断会标记，不将读取失败报告为零。
- Nginx仅统计共享access.log。AS Robot独立域名关闭access_log，不能称覆盖所有站点。来源IP数含爬虫，不是访客人数；Fail2ban按规则计数，不把同一IP的多规则封禁相加当作独立攻击者。
- 不运行交互式lnav/goaccess界面给助手；直接将相应日志汇成可读事实。完整人工排障仍可由维护者使用原工具。
- 本地发布包 /private/tmp/asrobot-health-20261105 ，包含Linux二进制、静态文件、manifest和REVIEW.md。它包含本轮之前尚未提交的提醒文案/标点修复，不得回退覆盖。服务器 services-deployed.json 与 deployed.json 保存实际上线证据；已核验运行进程摘要、服务状态、公开静态文件哈希及真实浏览器验证码刷新。


## 验证码刷新修复（同批上线）

刷新请求开始即清空旧验证码key和已输入答案，暂停登录；新图和key一并就绪后恢复。客户端请求明确no-store，验证码到期使旧key失效，手动刷新清理旧过期提示；登录失败后等待新挑战，防止继续使用已消费key。后端300秒TTL和SQLite一次性重放保护保持原样。桌面真实浏览器已验证新图/new key同时变化；过期、请求进行中提交和旧页面回包隔离使用合成场景回归，不代表完成真实用户密码登录。

当前原生程序摘要：reasonix 9488f91d1ba022849c1be316b1b6b455d78dc7a80631604b7b81fbd2b54dd24c；bridge 9db73cc63056e324c9e4d4459d466b68a6935cf2c31d358f161f6dc37658ab43。备份 /var/backups/asrobot-health-20261105-151216，原平台容器 asrobot-before-health-20261105 保留。未来操作前需重新核对。
