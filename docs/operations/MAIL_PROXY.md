# 邮箱访问代理

部署日期：2026-10-23，Asia/Shanghai。

服务器已安装独立的 Mihomo 代理，用于后续邮箱客户端显式调用。此部署只完成网络准备，没有授权邮箱、读取邮件、修改 Friday 模型配置或创建定时任务。

## 安装与范围

- 服务：`mybot-proxy.service`，已启用开机启动。
- 内核：Mihomo `v1.19.33`，Linux amd64 compatible 官方发行包。
- 下载包 SHA-256：`386508bfb1c72c43b6a0eda73708ecdce82546581b0ebcb16366c53757c622b6`，与官方 GitHub release asset digest 一致。
- 解压后二进制 SHA-256：`817f50b3ac6a8b1c4360c5cf2961906057f3a883d970ebbcb0028a01632b620f`。
- 程序：`/opt/mybot-proxy/current/mihomo`，指向 `/opt/mybot-proxy/releases/v1.19.33/`。
- 私有配置：`/etc/mybot-proxy/config.yaml`。只保存在服务器，不纳入 Git。
- 持久状态：`/var/lib/mybot-proxy/`。
- 独立系统用户：`mybot-proxy`，无登录 shell、无额外网络管理能力。

配置来自 Sir 指定的本机 Clash 配置的节点快照，含 19 个 Trojan 节点。原始本机配置未修改。服务器配置不包含原有的 1109 条完整分流规则，也没有迁移桌面设置；没有 proxy-provider 或 rule-provider 外部文件依赖。节点快照不自动追踪本机或订阅更新，节点失效时需重新同步并验收。

## 网络行为

- HTTP／SOCKS 混合代理：`127.0.0.1:17890`。
- 控制接口：`127.0.0.1:19090`，使用独立随机访问密钥；密钥不写入本文档。
- 仅显式指定代理的客户端请求经过此服务。
- `googleapis.com`、`google.com`、`gmail.com`、`gstatic.com` 的域名及子域名走节点，其他目标 `DIRECT`。
- TUN、透明代理及自定义 DNS 监听均关闭；没有更改系统默认路由、全局代理环境或防火墙端口。
- 没有给现有 `mybot-heartbeat.service` 添加代理环境或重启该服务。后续邮箱适配器应为其 HTTP 客户端配置此代理，不能假设所有 Friday 请求已自动切换。

代理配置目录权限 `0750`，配置文件 `root:mybot-proxy 0640`，状态目录由专用用户拥有且权限为 `0700`。控制接口密钥及节点凭据不得复制到日志、文档或项目配置模板。

## 验证结果

部署前通过内核配置校验与 systemd unit 校验；安装后已核对两个端口均只监听 `127.0.0.1`。新代理服务重启后再次验收：

| 检查 | 结果 | 含义 |
| --- | --- | --- |
| Google 登录公开配置 | HTTP 200 | 可以通过节点访问授权服务相关入口 |
| Gmail messages API（无凭据） | HTTP 401 | 能到达 API；没有账号授权，401 是预期结果 |
| Google token 端点 GET（无凭据） | HTTP 404 | 网络和 TLS 连接成功，不代表令牌交换已完成 |
| 中新网 RSS 经同一代理入口 | HTTP 200 | 默认直连分流的公开读取正常 |

测试没有使用邮箱令牌或读取个人邮件。另以 `mybot` 系统用户调用代理验证 Gmail API，结果记录在本次部署会话；现有 Friday 服务保持 active/running，检查时 `NRestarts=0`。没有重启服务器，不声称已经做过整机重启验收。

## 运维

```sh
sudo systemctl status mybot-proxy.service
sudo systemctl restart mybot-proxy.service
sudo systemctl is-enabled mybot-proxy.service
```

无凭据连通性检查，预期返回 `401`：

```sh
curl --silent --show-error --noproxy '' \
  --proxy http://127.0.0.1:17890 \
  --connect-timeout 7 --max-time 16 \
  --output /dev/null --write-out '%{http_code}\n' \
  'https://gmail.googleapis.com/gmail/v1/users/me/messages?maxResults=1'
```

如需停用，只停止此独立代理服务：

```sh
sudo systemctl disable --now mybot-proxy.service
```

这不会删除节点配置或个人邮箱数据；未来如果邮箱适配器依赖该代理，停用会使相应取数失败，需要明确呈现错误，不能误报“没有新邮件”。

官方资料：[发行项目](https://github.com/MetaCubeX/mihomo)、[Linux 服务](https://wiki.metacubex.one/en/startup/service/)、[代理入口](https://wiki.metacubex.one/en/config/inbound/port/)。
