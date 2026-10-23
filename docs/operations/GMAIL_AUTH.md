# Gmail 授权状态

日期：2026-10-23，Asia/Shanghai。

两个个人 Gmail 账号均已完成 Friday 项目的 OAuth 授权。使用 Desktop OAuth 客户端，在 Mac 的本机回调端口完成浏览器授权，校验随机 state 与 PKCE S256。请求范围仅为 `https://www.googleapis.com/auth/gmail.readonly`。

## 已验证

- 两个账号分别取得离线刷新凭据，账号身份独立核对。
- 凭据已通过 SSH 保存到服务器 `/var/lib/mybot/mail/gmail/`，每个账号独立文件；不纳入 Git。
- 配置目录由 `mybot` 用户拥有，目录权限 `0700`，凭据文件权限 `0600`。
- 两个账号均在服务器通过本机代理完成 refresh token 换取访问令牌，再成功调用 Gmail `users.getProfile`，确认身份与已保存账号一致。
- 刷新后的凭据已原子保存。此次没有读取邮件正文，也没有发送、删除或标记邮件。

服务器网络代理参见 [MAIL_PROXY.md](MAIL_PROXY.md)。本机授权所需的临时令牌副本在服务器验证成功后清理；用户下载的原始 OAuth 客户端 JSON 不自动删除。

## 尚待完成

- 核对 Google Auth Platform 发布状态。如果仍为 External / Testing，这类刷新令牌通常在七天后失效；首次授权通过不代表长期无人值守已验收。按个人自用适用规则核对发布／验证流程，必要时重新授权，不能假设切换状态就会延长已签发令牌的寿命。
- Friday 的邮件搜索、列出、正文读取和增量同步工具尚未实现或接入。凭据就绪不等于上述业务工具已经可调用。
- Outlook 与 QQ 邮箱尚未授权。
- 没有创建邮箱轮询任务或改变现有 Friday 模型、微信服务配置。

官方资料：[Gmail 只读权限](https://developers.google.com/workspace/gmail/api/auth/scopes)、[桌面应用授权](https://developers.google.com/identity/protocols/oauth2/native-app)、[刷新令牌有效期](https://developers.google.com/identity/protocols/oauth2#expiration)。
