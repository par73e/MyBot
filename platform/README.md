# AS Robot

私人助理控制台，提供账号管理、微信连接、对话、会话记录、待办、记忆和到期事项。后端使用 FastAPI 和 SQLite，前端使用原生 HTML、CSS 和 JavaScript。

## 本地开发

```sh
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt pytest
ASROBOT_DATA=/tmp/asrobot-dev ASROBOT_ORIGIN=http://localhost:18980 .venv/bin/uvicorn app.main:app --port 18980
```

账号、凭据和运行时数据应保存在仓库外。部署前应根据目标环境检查认证、网络、备份和隔离配置；测试环境的结果不代表生产系统通过完整安全审计。

## 助手实例与迁移

新账号使用独立的系统用户、私有目录和受认证的本地套接字，每位用户可以设置自己的助手名称、称呼和服务凭据。网页用于管理，微信承担日常对话和提醒。平台和助手之间的路由、配对及旧数据迁移必须与主机上的隔离服务一起部署；单独运行本地网页不会自动提供完整的微信助手。

`deploy/instance_host.py` 和 `deploy/instance_worker.py` 描述当前 Linux 主机模式；实际部署需要事先构建可信的助手与桥接程序、检查 systemd 隔离和网络约束，并由具备主机管理权限的运维人员安装。`instance_cutover.py`、`instance_migration_host.py` 和 `instance_cutover_recovery.py` 是受控迁移的底层工具，切换前需要停止旧调度器、制作新鲜的离线快照并验证归属。新平台开始接收用户请求后，不应自动恢复旧数据库覆盖可能产生的新数据。旧平台中已有数据的停用账号需要单独迁移，不能按空白账号接管。

`Dockerfile.instance`、`deploy/instance_launcher.py`、`deploy/instance_network.py` 和 `deploy/asrobot-instance@.service.example` 是早期容器方案的实验材料。当前主机上该方案不能满足所需的嵌套沙箱，应使用上述主机服务模式；实验文件不代表可直接上线的部署方式。

### 登录验证码与 Redis

登录页使用一次性图片验证码，5 分钟有效。连续 5 次密码错误后，账号在失败窗口内限制登录 15 分钟；同时保留现有 IP 和账号请求频率限制。成功登录的新会话固定在 3 小时后过期，浏览器 Cookie 和服务器端会话都采用相同期限；这不是 JWT，Redis 不保存会话令牌。退出和修改密码仍以 SQLite 会话撤销为准。

可配置 `ASROBOT_REDIS_URL`（Redis 6.2 及以上）启用验证码和失败次数的共享 Redis 状态。验证码和失败次数均有 TTL；SQLite 保留防重放和故障降级状态。Redis 不可达时使用 SQLite，恢复后新操作会再次使用 Redis；降级期间 Redis 中未能清理的旧失败计数在其 TTL 内可能暂时继续限制登录。Redis 不配置时也可用 SQLite，但此时没有 Redis 共享状态。客户端只使用有限超时，日志不记录验证码、密码或 Redis 地址。

部署时应提供认证并使用仅受信任的内部网络或 Unix socket，不对公网开放 Redis；连接凭据从受保护的运行环境注入，不要写入源码。容器部署需要给镜像构建 wheel 目录补充 `redis==5.2.1`，并核验应用实际可访问 Redis。升级前已存在的会话仍保留原到期时间；如需立即统一为新规则，应在维护窗口明确撤销旧会话，让用户重新登录。不要删除用户、业务数据或 Redis 中无关键。
