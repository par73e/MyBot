# 上海官方天气预警来源：本地与服务器实测

2026-10-26，北京时间15:23至15:29。本次为Sir授权的来源研究和一次性公开GET探测；没有接入业务、建立持续轮询、重启服务、修改生产状态或发送微信，也没有调用和风付费接口。

## 结论

相较同日早先的调查，本次已找到三个**无需Key、当前能直接读取、来自官方预警网页**的数据接口。三轮本地和三轮腾讯服务器请求跨约5分钟均成功；服务器关键列表单次耗时0.068–0.186秒。可以进入适配器与容错验证阶段，但不能把短期连通性当成长期稳定保证，或把不需要鉴权当作有正式免费开发者接口承诺。

目前上海三个列表均为空，故尚未以真实上海生效记录验证完整字段和覆盖。其他地区的真实生效预警详情读取成功，具有正文、类型、等级、发布时间、有效截止、identifier/references和msgType。未实测提前解除事件。正式自动推送尚未启用。

## 1. 上海专用源：中国天气网上海预警页

页面：https://sh.weather.com.cn/zhyj/

实际请求：

```text
GET https://d1.weather.com.cn/shweather/alarm.json
Referer: https://sh.weather.com.cn/zhyj/
```

这是该页面内`loadAlarm()`直接调用的数据源，固定JSONP回调`alarm_callback`。页面标注信息来源为上海市预警发布中心，制作维护为中国气象局公共气象服务中心、上海市气象服务中心。

本地/服务器各3次HTTP 200、解析成功；均为`alarm_callback([])`，18字节。服务器耗时0.068、0.087、0.070秒。响应含Last-Modified与ETag，额外条件请求实测304。最后修改时间为当天15:21:01，三轮未变，不能据此声称每分钟更新或严格新鲜度承诺；空列表没有预警事件时间戳可独立证明上游采集健康。

网页脚本预期每条记录含province/city/town/type/level/time/content/icon等，实际非空上海记录仍待验证。不能根据静态HTML的“暂无预警”认定无预警，因为HTML占位文字在数据加载失败时仍可能显示。页面沿用旧分区地图，也不能据地图展示推断2026中心城区新机制的覆盖完整性。

## 2. 中央气象台上海查询

页面：https://www.nmc.cn/f/alarm.html

实际请求与网页的参数结构一致：

```text
GET https://www.nmc.cn/rest/findAlarm?pageNo=1&pageSize=20&signaltype=&signallevel=&province=%E4%B8%8A%E6%B5%B7%E5%B8%82
Referer: https://www.nmc.cn/f/alarm.html
```

三轮本地/服务器均HTTP 200、有效JSON、`code=0`，`data.page.count=0`、`list=[]`，255字节；服务器耗时0.106、0.138、0.183秒。全国查询的正样本包含alertid、issuetime、title、url、pic、分页count/totalPage，当前真实详情页能读到发布机关与完整正文。省域查询适合发现上海全市及各区发布的信息，完整性仍需以非空上海数据验证，不能只看第一页或仅查询上海城市天气码。

初始省略signaltype/signallevel的简化请求曾得到417，按网页完整参数请求后成功。实现应固定官网参数结构，HTTP异常保留未知状态，不能改成空集；没有绕过验证码或访问控制。

## 3. 中国天气网全国列表与结构化详情

官方页面：https://www.weather.com.cn/alarm/

其加载的脚本`https://j.i8tq.com/newAlarm/index.js?2022061937`使用：

```text
GET https://product.weather.com.cn/alarm/grepalarm_cn.php
GET https://product.weather.com.cn/alarm/webdata/<列表返回的html文件名>
Referer: https://www.weather.com.cn/alarm/
```

全国列表是受限格式`var alarminfo=<JSON>`，三轮本地/服务器均成功，约159KB；总数从745变为744，说明不是固定示例，上海匹配项始终为0。服务器耗时0.186、0.162、0.170秒。列表里的发布地域、详情文件名和国家预警identifier可用于地区过滤和追溯；与中央气象台全国总数不同，不能假定两条发布链覆盖/时间完全一致。

使用列表提供的齐齐哈尔大风蓝色预警详情，服务器HTTP 200、922字节，实测含`SIGNALTYPE/SIGNALLEVEL/ISSUETIME/RELIEVETIME/ISSUECONTENT/EFFECT/msgType/identifier/references`。本次msgType仅观察到Alert；不能因为字段存在就宣称Update/Cancel均已验证。RELIEVETIME按来源有效截止使用，不能承诺届时官方正式解除。两条管线可能共享上游，不能视为完全独立数据来源。

只按列表严格校验的文件名组装固定Host详情URL，JSON/JSONP只剥离允许的固定包装后解析，不能执行远端JavaScript。

## 不作为本轮主源的渠道

- 全国地图`https://forecast.weather.com.cn/api/v1/traffic/alarm/alarmMap?callback=getAlarm`：本地和服务器HTTP 200，但包含大体积地图信息，超过本次1.5MB读取上限，主动停止，没有验证完整结构；轻量提醒没必要反复取整张地图。
- 上海城市天气页使用`https://d1.weather.com.cn/dingzhi/101020100.html`，服务器200、248字节，`alarmDZ101020100.w=[]`。单城市码是否覆盖全市与各区不能由空样本证明，不替代省域查询。
- 上海市气象局网站引用的`http://smb.shweather.cn:5678/smb/Home/GetAlertListByType?type=sh`：本地与服务器仍超时。
- 12379主页服务器仍超时。WMO注册目录列出的`http://www.12379.cn/rss/china_rss.xml`本地/服务器均404。不能把已登记的旧RSS地址当作仍可用订阅。
- 上海开放数据目录仍是申请正式开放渠道的线索，此次没有新增实测可用API证据；保留此前记录。

## 接入建议与尚待验证

技术上优先做中央气象台上海JSON列表与上海专用源的组合；全国列表/详情用于核验与补充，不必每次下载地图。候选节奏为作息内每5分钟，全局共享缓存、串行或小并发、错误退避；新预警及实质等级变化以程序比较，未变化不调用模型。这个节奏是工程候选，不是已查到上游对第三方的频率授权。

必须保留成功空集、失败、陈旧、来源冲突四种状态；失败不能清空现有预警，列表消失不能直接通知“官方解除”，早于截止消失需进一步核对。若两个源均失败，保留最后状态并标记覆盖不完整。上海当前为空，因此真实上海非空字段、区级/中心城区覆盖、更新与提前解除、较长时间可用率和端到端延迟都尚未完成验证。

2026年上海官方规定区分全市、中心城区和区级预警，接入不应仅按“上海市”标题完全匹配或固定老区名过滤。依据：[上海市政府2026年发布与传播规定](https://www.shanghai.gov.cn/nw12344/20260601/5fd69c1c43f24ecbb3a7387f62b12194.html?siteId=1)。

## 使用边界与正式渠道

上述网页数据目前不需要Key，但没有找到适用于第三方5分钟轮询的公开配额、服务级别或兼容性承诺。中国天气网网页及中央气象台网页均有使用声明，不能将可访问等同于开放数据许可。中央气象台[网站声明](https://www.nmc.cn/publish/cms/view/722665831f0a4e98800c41e691444963.html)与页面页脚需一并核对。若要正式接口和明确使用条件，中国天气网[SmartWeatherAPI说明](https://www.weather.com.cn/wzfw/smart/weatherapi.shtml)提供申请途径，但该页未证实当前免费权限和5分钟频率，不能预先承诺。

WMO[中国预警机构登记](https://alertingauthority.wmo.int/authorities.php?recId=30)所列RSS已实测404。WMO[SWIC说明](https://severeweather.wmo.int/note.html)明确其资料可按来源署名再利用，也明确CAP缺失/不可访问时地图空白不代表没有预警；本次未验证其上海数据链，不能拿来充当已完成的替代源。

实测明细见同名JSON。`probe-weather-alert-sources.py`为一次性研究脚本，固定公开GET、短超时、响应上限，仅输出研究结果；不是生产采集器或定时任务。原调查文档保留为当时快照。
