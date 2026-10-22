# 腾讯服务器部署验收

时间：2026-10-22 17:47（北京时间）；目标：SSH 别名 `tencent`，Linux x86_64。
安装版本：`v1.39.10-heartbeat.1`，上游基础 `v1.39.10`。

本报告记录最初部署的验收快照。后续已接入真实模型和微信网关，
见 [模型与微信配置报告](MODEL_WEIXIN_SETUP.md)。

## 结果

- 安装包和二进制 SHA-256 验证通过，服务器二进制与本地交付完全一致。
- `mybot-heartbeat.service`：`active/running`、`enabled`，独立 `mybot` 账号。
- 真实 systemd 服务按时执行一次性固定消息任务，保存 `completed`，并自动停用。
- 重启服务后历史仍为一次，没有重复运行。
- 主动终止测试进程后，systemd 在 5 秒后自动恢复；`NRestarts=1` 是本次测试造成。
- 用相同的服务权限／资源限制执行 Linux 二进制端到端测试：一次模型请求，
  两次模拟发送（第一次 503，第二次成功），重启不重复，固定提醒零模型请求。
- Linux bwrap 只读和网络隔离检查通过。
- 验收任务 `6c501a9648b9d5cb` 已删除，最终生产任务列表为 `[]`。
- 检查时空闲服务内存约 9.7 MiB，整机可用内存约 2.87 GiB。
  此数据不代表模型、邮箱、浏览器等完整业务的峰值占用。
- AURORA 与 Nginx 保持运行，PID 与部署前一致（分别为 953、1064）；
  没有修改其配置，没有新增监听端口。

## 校验值

```text
676904f2fd4a632756074d523de36c626d52c04ab54c00ca148f7fb1f13e3372  /opt/mybot/current/reasonix
11d39094f044f0cda0febb3ad517b830301da6b2ce929664af09ee9e4dd264f9  /etc/apparmor.d/bwrap-userns-restrict
88be543dabed2e9dd63daca656757e331f9b14eff2f29ca871185a30f502449b  deployment bundle
```

运行说明和依赖处理见 [服务器使用说明](SERVER_GUIDE.zh-CN.md)。
模型测试使用本地模拟接口；真实 API Key 和微信尚未接入，没有消耗真实模型额度，
没有发起扫码授权或向真实微信会话发送消息。
