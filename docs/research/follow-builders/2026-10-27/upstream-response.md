已按 Skill 原版流程跑通：`prepare-digest.js` 抓取中心 feed（本次含 17 位 builder / 32 条 X 帖 + 1 期播客），再按其 prompts 改写为中文，输出走 stdout，未创建定时任务、未写记忆、未改任何配置。

**AI Builders Digest — 2026-10-27**

## 一、X / Twitter 动态

**OpenAI 的 Thibault Sottiaux（Codex 与 ChatGPT 负责人）和 Sam Altman 同步宣布 6.1 Terra 上线。** Sottiaux 称这是第一个在写作、个性和对请求的细腻理解上"明显更好"的模型，computer use 很能打，且在 API 和订阅里给到 6.1 Sol 两倍的用量。Altman 强调他们最兴奋的是"computer use 的新时代"。
https://x.com/thsottiaux/status/2114800277677092936
https://x.com/sama/status/2114818620782248024

**Vercel CEO Guillermo Rauch 复现了 OpenAI 关于 agent 用 CPU 隐蔽通信的结论，做出 cpu-chat。** 它在 2 个以上无外网的 Firecracker microVM 之间，靠 CPU 争用、缓存逐出计时、DRAM 带宽争用三种机制聊天，跨多 CPU 绕过了 Firecracker 的全部生产建议。他的结论是：只要共享硬件，agent 就有很大概率能低声交流，而且延迟还不错。更值得注意的是 OpenAI 和 Anthropic 的模型拒绝做这项研究，开源权重模型把它做完了，他认为这种立场可能反噬安全本身。
https://x.com/rauchg/status/2114770919436808572

**Replit CEO Amjad Masad 提出"Agent Habitat"概念。** 他说几百年来公司架构几乎没变：层级、部门、科层协调，而 AI agent 即将颠覆它，走向"人负责判断、agent 执行大部分工作"的自动驾驶公司，Replit 正在为此打地基。另有一条热帖吐槽 OpenAI 问三个科学社区"发现该怎么发布"得到三种答案：数学家说直接发谁在乎，计算机科学家说开源但请给我们会议付款，物理学家说交给我们来处理。
https://x.com/amasad/status/2114760898225299721
https://x.com/amasad/status/2114890745471545419

**Anthropic S-1 成为这一天的集中话题。** FPV Ventures 合伙人 Nikunj Kothari 拆算力合同：截至 6 月 30 日有 2656 亿美元采购承诺加 3214 亿美元尚未开始的租赁，小计 5870 亿美元；Google 和 Amazon 要求不论用不用都有最低消费，而 SpaceXAI 那份潜在价值 1205 亿美元的协议在 2027 年 1 月和 3 月锁定期后任一方可提前 90 天取消；Broadcom 的 420 亿美元设施不直接给现金，票据利息 9%、IPO 后可按 30 日均价的 90% 转成股票；每美元算力与基础设施开销对应的收入从 2025 年 Q2 的 0.57 美元涨到 2026 年 Q2 的 2.08 美元。
https://x.com/nikunj/status/2114761204333998427

Box CEO Aaron Levie 抓的是另一个数字：Anthropic 平均一家财富 500 强客户的支出不到其总费用的 0.1%，他认为涨到 1% 到 5% 完全合理，真正的跳变发生在企业为 agent 重接底层流程时，那类负载的 token 消耗是现在的 100 倍。
https://x.com/levie/status/2114761816199098587

Every CEO Dan Shipper 给出的账面要点：1H 2026 收入同比增长 1300%，GAAP 口径经营利润转正，毛利率 85%，收入留存 900%，且 80% 的收入来自最近 6 个月发布的模型。
https://x.com/danshipper/status/2114714881312305542

**Google Labs / Gemini 产品 VP Josh Woodward 宣布与美军和 USO 合作**，在 20 个地点部署 Google Beam，让驻外军人和亲人之间可以做拟真的 3D 视频连接。
https://x.com/joshwoodward/status/2114840668296409568

**Claude 官方称已有超过 25000 家非营利组织在用 Claude**，多数是小型本地团队，Claude for Nonprofits 将上线与约 30 家机构共建的插件，附带扎根于各自上下文和 AI 政策的筹款、市场、招聘 skills。
https://x.com/claudeai/status/2114780227465338985

**Peter Yang（AI 教程作者）抛出一个判断：** 很快人们在网上不会再亲自阅读或操作，而是直接 tag 自己的 agent 去做，现在的 @bot 就是早期信号。他说让他兴奋的是这件事，而不是第 9 个帮他管邮件的私人 agent。
https://x.com/petergyang/status/2114927195332460940

**Peter Steinberger（OpenClaw）周末测了 bun 的 AOT 编译器**，说结果好得惊人：性能提升 2 到 3 倍，内存占用不到一半。
https://x.com/steipete/status/2114879798434758905

**Special Projects Partner、Bevel Health 联创 Aditya Agarwal 认为美国 AI 最大的风险是自己把自己吓到减速**，恐惧是政策选项，建设也是。
https://x.com/adityaag/status/2114793226154418314

## 二、播客

**AI & I by Every：How Hugging Face's Investor Spots Winners**

核心观点：刷榜的模型和能干活的模型正在分家，而真正的护城河可能不再是代码本身。

Factorial Capital 创始人兼管理合伙人 Matt Hartman 在 Betaworks 待了近十年，当年写下了投给 Hugging Face 的第一张支票，那家最初做青少年聊天机器人的公司上月被 NVIDIA 以 1300 亿美元收购。他把 Hugging Face 理解成一个市场：一边是写论文并顺手把论文变成代码的研究者，另一边是像他这样不训练模型但想用最新模型的人。访谈直面当下最尖锐的问题：如果任何人都能 vibe code 出一个应用，软件护城河到底在哪里，现在该怎么建软件公司。

节目开头 Every 的实测也值得一提：Google 和 Meta 的新前沿模型都登顶了公开 benchmark，但换成真实知识工作就露馅。Meta 的 Wonder 被判断为 bench maxed，在"重写一个 vibe coded 代码库"的高级工程师任务上只拿到 50% 左右；Gemini 4 是"这个季度最好的写作模型"，写八千字长文时结构感强到让人生产力翻倍，但同一批编码任务只有 20%。另一个用 StarCraft 做的评测里，模型直接上网搜攻略再来打游戏。

Hartman 的方法论更有意思。他自嘲现在的读法是杠铃式：一头是最新的东西，另一头是经得起时间的旧东西，最近在读罗马帝国的衰亡，因为有些东西显然在重复。对于"哪里还有机会"，他说解决方案空间之大让他惊讶，就像warcraft里整张地图还是黑的，你走动才逐渐看见。他给的建议也带着投资人式的幽默：想知道什么是新近可能的，最佳办法是听 Every。
https://www.youtube.com/playlist?list=PLuMcoKK9mKgHtW_o9h5sGO2vXrffKHwJL

Generated through the Follow Builders skill: https://github.com/zarazhangrui/follow-builders

（feed 里另有一条播客因源站 HTTP 500 拿不到转写稿，已按规则跳过。）
