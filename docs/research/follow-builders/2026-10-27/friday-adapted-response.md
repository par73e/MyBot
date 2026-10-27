Sir，Follow Builders 试用结果如下。

数据来自作者集中维护的公开 feed 快照，更新时间为北京时间今天 14:44（不是每条内容的发布时间，且不代表已抓全最新帖子）。本次快照含 17 位 builder 的 32 条 X 帖子、1 期播客；官方博客这一路为空，另有 1 期播客转写稿抓取失败（Ian Quigley 那期，HTTP 500）。以下 5 条为本次 feed 中值得关注的动态，均为上游作者转述，我没有独立核实：

**1. Anthropic 递交 S-1，投资人们在拆文件**
这是本次 feed 里出现频率最高的话题。Dan Shipper 罗列了他从 S-1 里读到的数字：2026 上半年营收同比增长约 1300%、GAAP 经营性盈利、约 85% 毛利率、900% 收入留存，并称约 80% 收入来自最近 6 个月发布的模型（https://x.com/danshipper/status/2114714881312305542）。投资人 Nikunj Kothari 更关注算力合同：截至 6 月 30 日有 2656 亿美元采购承诺和 3214 亿美元尚未起租的租赁，合计约 5870 亿美元，且 Google、Amazon 要求不管用不用都有最低支出，而某家协议虽潜在价值达 1205 亿美元但双方提前 90 天通知即可取消（https://x.com/nikunj/status/2114761204333998427）。Matt Turck 则调侃 Anthropic 特意挑周一早上递交，让 VC 们来不及写解读（https://x.com/mattturck/status/2114755282181955616）。值得关注的是：这些是市场叙事转向"看财报细节"的信号。

**2. 一个名为 Terra 的新模型在 X 上被集中宣传**
OpenAI 的 Sam Altman 发帖称 Terra 已上线，并说最令他兴奋的是"计算机使用（computer use）的新阶段"（https://x.com/sama/status/2114818620782248024）；Thibault Sottiaux 的版本称该模型在写作、个性和对请求的细腻理解上明显更好，且在 API 与订阅中提供的用量是前一版本的 2 倍（https://x.com/thsottiaux/status/2114800277677092936）。两人措辞里的版本号不一致，feed 里没有官方发布页，具体参数请以官方为准。

**3. Replit CEO Amjad Masad 提出"Agent Habitat"：押注"自动驾驶公司"**
他认为几百年来公司架构几乎都是"层级 + 部门 + 官僚协调"，而 AI agent 会打破它：人类负责判断，agent 承担大部分执行，Replit 正在为此打地基，他称之为 Agent Habitat（https://x.com/amasad/status/2114760898225299721）。同一天他还转了个段子，说 OpenAI 就"如何发布科学发现"问了三个学术群体，数学家说"直接发吧"，计算机科学家说"开源但也得给我们付会议费"，物理学家说"交给我们，书呆子"（https://x.com/amasad/status/2114890745471545419）。

**4. Vercel CEO Guillermo Rauch 复现了"CPU 隐性通信"**
他做了一个叫 cpu-chat 的复现：在没有网络、只有 2 个以上 Firecracker microVM 的通用主机上，通过 CPU 争用、缓存逐出时序、DRAM 带宽争用三种机制实现跨 CPU 通信，绕过了 Firecracker 的全部生产环境建议（关 SMT、关 KSM、租户绑核）。他的结论是：只要涉及共享硬件，你的 agent 之间很可能存在延迟相当低的隐蔽通信通道（https://x.com/rauchg/status/2114770919436808572）。这对多租户 agent 沙箱是个真实的架构警告。

**5. Anthropic 宣布扩展 Claude for Nonprofits**
Claude 官方账号称已有超过 2.5 万家非营利组织使用 Claude，多数是小型本地团队；将与约 30 家组织共建的插件纳入其中，覆盖筹款、市场、招聘等场景，并绑定各团队自身的上下文和 AI 使用政策（https://x.com/claudeai/status/2114780227465338985）。

可选延伸阅读：播客《AI & I by Every》10 月 14 日那期"How Hugging Face's Investor Spots Winners"，嘉宾是早期投了 Hugging Face 的 Factorial Capital 管理合伙人 Matt Hartman，聊"人人都能 vibe code 出 app 时软件护城河在哪"。feed 里这个链接只是 YouTube 播放列表而非单集页（https://www.youtube.com/playlist?list=PLuMcoKK9mKgHtW_o9h5sGO2vXrffKHwJL），所以没法直接推荐到某一期。

本次为单次试用，没有创建任何定时任务或改动配置。数据来自 Follow Builders，仓库：https://github.com/zarazhangrui/follow-builders
