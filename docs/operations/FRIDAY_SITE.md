# Friday 公开说明页

2026-10-23（Asia/Shanghai）已部署静态页面，用于 Google Auth Platform 的应用介绍和数据使用说明。

| Google 字段 | 填写值 |
| --- | --- |
| 应用主页 | https://par73e.cn/friday/ |
| 应用隐私权政策链接 | https://par73e.cn/friday/privacy.html |
| 应用服务条款链接 | https://par73e.cn/friday/terms.html |
| 已获授权的网域 | par73e.cn |

源码位于 `web/friday/`；服务器仅公开其中三个 HTML 文件和 `site.css`，目录为 `/var/www/friday/`。Nginx 的 HTTPS 虚拟主机通过 `/etc/nginx/snippets/friday.conf` 提供 `/friday/` 路径，未改动其他路由。`nginx.conf` 是运维文件，不上传至公开目录。

部署前配置备份：`/var/backups/mybot-friday-site/20261023T075559Z/aurora-demo.before`。恢复此备份前应先比较期间是否有其他配置更新，不可直接覆盖共享配置。撤销本页面只需移除 HTTPS 虚拟主机中的 Friday include，执行 `nginx -t` 通过后 reload；公开目录与独立 snippet 可随后单独清理。

已验证：四个公开文件 HTTPS 200、内容 SHA-256 与本地一致，主页到隐私政策和服务条款的浏览器跳转正常；原有 `/aurora/`、`/resume/`、`/cyz/` 返回 200。Nginx 配置验证通过，reload 后 Nginx 与 Friday 服务均 active，NRestarts=0。

网页不含 JavaScript、表单、第三方统计或邮箱凭据。公共联系邮箱采用用户在 Google Cloud 已选择的支持邮箱。

## 后续配置边界

页面上线不代表 Google 项目已发布或已通过验证。需要在 Google 控制台填写上述字段并保存，查看目标对象中的发布状态；若要求域名所有权验证，按 Google Search Console 给出的实际记录操作。没有代替用户提交发布或验证。

邮件查询、正文读取和摘要功能仍未接入；后续启用邮件 AI 分析前，需要明确具体模型服务与数据范围，确认该服务的数据处理约定满足公开政策，并向账号所有者告知、取得同意。当前政策不能用来声称第三方模型服务已满足要求。功能、数据接收方或保留方式变化时，应同步更新网页。
