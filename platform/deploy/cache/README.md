# AS Robot 页面读取缓存

2026-11-05 已经用户确认部署上线。平台镜像 asrobot:page-cache-20261105。AURORA 未修改。

## 范围

仅在平台已经认证、校验账号状态并解析真实助手绑定后，缓存原生桥接的少数成功读取结果。HTTP 页面和认证接口继续使用 Cache-Control: no-store；Redis 是后端读取加速层。

| 读取 | 最长 TTL |
| --- | --- |
| 工具目录 | 60 秒 |
| AI 新闻候选列表 | 20 秒 |
| AI 新闻已选择来源正文 | 60 秒 |
| GitHub 跟踪状态、单条 PR 查询 | 10 秒 |
| 最近晨报、晚间总结 | 10 秒 |

TTL 会略微随机缩短，避免一批记录同时到期。待办、个人记忆、权限、登录、验证码、连接凭据、课表、作息、天气预警和健康采样不进入缓存。天气模块自身已有来源缓存，这一版不重复包缓存。

用户、原生实例绑定、操作与全部参数都参与缓存键；用户与参数使用摘要，键不直接暴露账号标识或参数。不同用户的内容不共享。只有允许列表中的成功 dict/list 结果会写入，单条上限 256KB，异常和错误结果不缓存。缓存命中也不能跳过每次请求的认证/绑定检查。

## 更新与故障

网页修改、连接设置更新及经平台发起的对话，在调用前后更新该用户/实例的随机 generation；未完成的旧读取只能写入旧 generation，不能重新污染新一代缓存。无需扫描 KEYS 或批量删除。旧 generation 及内容都会自行过期；generation 被 LRU 淘汰后使用新的随机值，不回到固定零版本。

微信直接修改、原生定时任务完成和上游数据变化不经过平台写入钩子：可缓存页面最多延迟上表 TTL。界面继续保留原数据时间，不将缓存时间冒充数据更新时间。请求头 X-Cache-Refresh: 1 可跳过读取旧缓存并重新填充；不改变权限或限流。

同一进程内有固定数量的异步锁合并并发未命中，避免重复请求原生来源。多个平台进程可以共享 generation，但不保证跨进程仅一次回源；当前平台单工作进程。Redis socket 读写/连接超时为75ms，读取失败短暂绕过缓存；写失效失败后至少绕过65秒，覆盖旧数据最长60秒寿命。Redis不可用时继续原生读取，不把它变成事实存储。

## 运行边界

新增独立 asrobot-page-cache.service：只监听 /run/asrobot-page-cache/redis.sock，最大64MB，allkeys-lru，最多64连接，不启用RDB/AOF。它与现有验证码/密码错误 Redis 是两个进程，内存淘汰不影响登录保护。

复用宿主机 asrobot-history 的10001组，与平台容器既有10001用户/组一致，不新增账号权限。平台只需只读挂载socket目录并设置：

```
ASROBOT_PAGE_CACHE_URL=unix:///run/asrobot-page-cache/redis.sock?db=0
```

代码不需要新增依赖，复用现有 redis-py 5.2.1 的异步客户端。未设置环境变量时完全跳过缓存。

## 研究参考

- 本地CSTS ProductCacheService：先读缓存、未命中回源、TTL抖动、异常降级；本版采用generation失效，避免它的KEYS扫描。
- https://github.com/long2ice/fastapi-cache ：明确命名空间、完整参数缓存键和过期控制；本版直接使用已有redis-py，以保留原生授权和写操作边界，不增加装饰器依赖。
- https://redis.io/docs/latest/develop/clients/redis-py/ ：官方客户端。

## 发布与回退

先保存当前容器、镜像与配置，新建缓存socket服务；在当前asrobot:health-20261105镜像上只覆盖 app/main.py、native.py、page_cache.py。新容器复制所有原参数，仅新增缓存环境变量和socket目录挂载。保留旧容器，切换平台短暂中断网页连接。无需重启Friday实例或验证码Redis，不更改数据库。

先检查Unix socket权限，再验证缓存命中、用户隔离、修改后失效、注销后访问拒绝、Redis断连回源与原有接口/验证码。故障时切回旧容器；保留原数据库、实例状态及备份，停用新增缓存服务即可。


## 本次验证与发布记录

- 全平台 Python：217通过、5跳过。另有真实隔离Redis验证缓存命中、过期、用户/实例隔离、并发合并、写入竞态、损坏缓存和断连回源，以及HTTP注销与绑定变更后的访问拒绝。
- 线上从已授权所有者工具目录完成重复读取：第二次确认为缓存命中；generation失效后再次回源；不存在socket的故障路径仍返回原始来源结果。一次内部采样首次108.16ms，命中2.83ms，仅代表该工具目录的单次检查，不代表整站网页或长期性能。
- 发布证据：/opt/asrobot/releases/page-cache-20261105/deployed.json；备份：/var/backups/asrobot-page-cache-20261105-155610；原容器：asrobot-before-page-cache-20261105。
- 平台仅新增ASROBOT_PAGE_CACHE_URL与Unix socket只读挂载，其余环境、身份、挂载与限制对比通过。Friday实例、验证码Redis未重启。
- 代码尚未提交Git，未修改Basic Memory。当前配置与数据未来可能变化，后续操作先核对现场。
