# Friday GitHub凭据准备与功能边界

日期：2026-10-26（Asia/Shanghai）。状态：凭据已准备并完成只读接口验证；**GitHub PR跟踪模块尚未实现、未部署、没有自动监控任务**。Sir已要求暂停开发，先收尾并交接。

用户自行生成细粒度PAT并选择不设到期日。本地`githubtoken.txt`权限为0600，根`.gitignore`排除`/githubtoken`及`/githubtoken.*`；文件没有进入Git。服务器凭据位置为`/var/lib/mybot/reasonix/credentials/github-pr-monitor.token`，服务账号持有、文件0600、目录0700。这里只记录存放位置和使用边界，不保存令牌原文、摘要、私人owner标识或任何聊天正文。

本地用该令牌读取GitHub官方API，身份、rate_limit、公开PR列表、普通评论、正式审核、行内评论、当前提交的check-runs和commit status均返回200。服务器随后以实际mybot服务账号成功读取凭据并验证身份、额度和公开PR读取。主额度为每小时5000请求，这是额度上限，不是剩余量；仍需遵守端点和次级限流。上述只读验证没有试验写权限，不能据此证明令牌完整权限清单。

这一步只准备凭据，没有增加assistant_github工具、PR关注清单、调度触发器、增量游标、事件去重或微信通知，也没有因凭据配置重启服务。正式接入时从受保护文件读取，不写入命令行、日志、源码、模型上下文或Basic Memory，不用这个读取用途令牌发布代码。

实现方案见`FRIDAY_ASSISTANT_EXPANSION_DESIGN_2026-10-25.md`第9节，仍是待开发设计。首版建议按用户明确添加的公开PR读取具体端点；不使用要求更广授权的通知收件箱接口，不把Codex桌面的GitHub连接当作Friday服务器集成。实际接口/权限依据实施当日官方文档再核验。

参考：
- https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
- https://docs.github.com/en/rest/checks/runs
- https://docs.github.com/en/rest/activity/notifications
