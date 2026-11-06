<a id="top"></a>

<div align="center">
  <img src="docs/images/asrobot-banner.svg" width="100%" alt="AS Robot — 让私人助手，各归其位。" />
  <h1>一个属于你的 Robot。<br />一段延续到日常的对话。</h1>
  <p>在网页里认识它，在微信里与它相伴。<br />让偏好被记住，让琐事有着落，让重要的信息适时出现。</p>
  <br />
  <a href="https://asrobot.par73e.cn/asrobot/"><img src="https://img.shields.io/badge/探索_AS_Robot_↗-6550D9?style=for-the-badge" alt="探索 AS Robot" /></a>
  <p><sub>邀请码内测 · 持续生长中的个人 AI 助手项目</sub></p>
  <p><a href="#关于这件作品">关于作品</a> · <a href="#它想做好的三件事">设计主张</a> · <a href="#从认识你的-robot-开始">开始探索</a> · <a href="#写给开发者">源码与技术</a></p>
</div>

<br />

## 关于这件作品

我们每天都会遇到一些很小、却值得被认真对待的事：一个突然想到的安排，一项不想反复解释的偏好，一条想继续关注的信息。

**AS Robot 想为这些日常，提供一个持续的落点。**

它把网页中的个人工作区与微信里的自然对话连接起来，让记忆、提醒和信息整理围绕同一个人展开。你可以为它命名，选择它的模型，慢慢形成自己的使用方式。

<br />

## 它想做好的三件事

### 01 · 交流，有延续。

让重要的偏好和安排留下来，下一次见面时，不必每件事都从头说起。

### 02 · 陪伴，有节奏。

从一句提醒到一天的收尾，尝试跟上你的生活，也尊重你不想被打扰的时刻。

### 03 · 助手，有自己的样子。

名称、称呼和表达习惯由你决定。每位用户，都有独立的助手与工作区。

<br />

## 两个入口，同一段日常

**网页，留给整理与掌握。** 在自己的工作区里配置助手，查看记录，调整安排。

**微信，留给随时的交流。** 当一个想法出现、需要一句提醒，或想看看最近值得关注的事，就从对话开始。

记住什么、关注什么、什么时候联系你，逐渐构成你与这个 Robot 的相处方式。

<br />

---

<div align="center">
  <h2>从认识你的 Robot 开始</h2>
  <p>为它取个名字，也给日常留一点新的可能。</p>
  <p><a href="https://asrobot.par73e.cn/asrobot/"><strong>前往 AS Robot，自己探索 →</strong></a></p>
  <sub>目前采用邀请码封闭测试，部分能力需要配置模型或连接第三方服务。<br />模型调用及部分服务可能产生费用，新账号开通仍在完善。</sub>
</div>

<br />

## 写给开发者

**AS Robot** 是产品名，**MyBot** 是本仓库名；**Friday** 是所有者为自己的助手选择的名字。

项目以 **Go / Reasonix** 为助手引擎，使用 **FastAPI、SQLite 与原生 Web** 构建管理平台，连接用户独立的助手实例与统一调度。产品正在真实使用中持续迭代。

[平台开发说明](platform/README.md) · [助理架构](reasonix-assistant-v1.39.10/ASSISTANT.md) · [上游与源码来源](reasonix-assistant-v1.39.10/SOURCE_PROVENANCE.md) · [讨论与反馈](https://github.com/par73e/MyBot/issues)

<details>
<summary>项目状态与使用边界</summary>

<br />

当前仍处于内测阶段，不承诺完全自助开通、所有自然表达都被准确理解，或全部真实账号与长期运行场景均已完成验收。第三方能力以实际接入、授权及可用数据为准；具体执行以工具成功结果为准。

校历与未来调休可能存在待核实日期，通用个人日历账户同步尚未接入；官方天气预警当前主要面向上海及其地区。邮箱和公开 PR 跟踪以读取、整理与通知为主，不自动发送邮件、发表评论或合并 PR。

前台对话全天可用，自动联系受已配置作息约束。独立实例不等于整机灾备已验收；健康页面或 HTTP 200 也不代表所有业务与微信送达正常。生产凭据与用户数据不随源码分发。

当前实现位于 [`reasonix-assistant-v1.39.10/`](reasonix-assistant-v1.39.10/)；[`platform/`](platform/) 包含网页、实例桥接和部署组件；[`reasonix/`](reasonix/) 为历史快照。单独启动网页不等于部署了完整助手。

</details>

<br />

<div align="center">
  <img src="platform/app/static/asrobot-icon-64-20261105.png" width="32" height="32" alt="AS Robot" />
  <p><strong>让私人助手，各归其位。</strong></p>
  <sub><a href="LICENSE">Apache-2.0</a> · Reasonix 组件沿用其 MIT 与第三方许可声明 · <a href="#top">回到顶部 ↑</a></sub>
</div>
