"use strict";
// User-facing views over the existing owner-scoped native bridge. No shadow state.
const robotModules = {
  assistant_tasks: [
    "待办与提醒",
    "把要做的事记下来，在合适的时间收到提醒。",
    "个人安排",
  ],
  assistant_memory: [
    "个人记忆",
    "让助手了解你的偏好，也能随时纠正或忘记。",
    "个人安排",
  ],
  assistant_subscriptions: [
    "到期管理",
    "会员、域名、证书和优惠券，到期之前心里有数。",
    "个人安排",
  ],
  assistant_proposals: [
    "待办建议",
    "查看助手从邮件中发现的事项，由你决定是否记下。",
    "个人安排",
  ],
  assistant_courses: [
    "课程表",
    "查看课程与教室，管理学期安排和临时调课。",
    "学习与资讯",
  ],
  assistant_ai_news: [
    "AI 新闻",
    "阅读最近一期 AI 简报，继续查看感兴趣的来源。",
    "学习与资讯",
  ],
  assistant_github: [
    "GitHub",
    "跟进公开 PR 的讨论、审核和检查结果。",
    "学习与资讯",
  ],
  assistant_mail: [
    "邮箱",
    "阅读最近邮件，管理邮箱连接和自动检查。",
    "生活服务",
  ],
  assistant_weather: ["天气", "查看关注地点的天气，为出行做准备。", "生活服务"],
  assistant_weather_alerts: [
    "天气预警",
    "关注上海官方预警及其更新、解除情况。",
    "生活服务",
  ],
  assistant_morning: ["晨报", "用天气、课程和重要事项开启一天。", "自动服务"],
  assistant_evening: [
    "晚间总结",
    "回顾未完成的事，提前看看明天的安排。",
    "自动服务",
  ],
  assistant_heartbeat: [
    "作息与自动安排",
    "决定助手什么时候可以主动联系你、做哪些事。",
    "自动服务",
  ],
  assistant_server_health: [
    "服务健康",
    "了解助手服务及运行资源是否需要关注。",
    "自动服务",
  ],
};
Object.entries(robotModules).forEach(([id, [name]]) => {
  nativeNames[id] = name;
});
const robotDay = () =>
  new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
function robotTime(v) {
  if (!v || String(v).startsWith("0001-")) return "尚无记录";
  const d = new Date(typeof v === "number" ? (v < 1e11 ? v * 1000 : v) : v);
  return Number.isNaN(d.getTime())
    ? "时间未知"
    : d.toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", hour12: false });
}
function robotLocal(v) {
  if (!v || String(v).startsWith("0001-")) return "";
  const d = new Date(v);
  return Number.isNaN(d.getTime())
    ? ""
    : new Date(d.getTime() + 8 * 3600000).toISOString().slice(0, 16);
}
const robotISO = (v) => (v ? v + ":00+08:00" : "");
const robotEmpty = (text) => `<p class="empty">${esc(text)}</p>`;
const robotPanel = (title, body) =>
  `<section class="panel"><h2>${esc(title)}</h2>${body}</section>`;
const robotError = (e) =>
  `<p class="inline-error" role="alert">${esc(e?.message || e)}。请稍后刷新重试。</p>`;
const robotText = (v) => (typeof v === "string" ? v : "");
const robotRows = (v) => (Array.isArray(v) ? v : []);
function robotLink(url, label) {
  try {
    const u = new URL(url);
    if (u.protocol !== "https:") return esc(label);
    return `<a href="${esc(u.href)}" target="_blank" rel="noopener noreferrer">${esc(label)}</a>`;
  } catch {
    return esc(label);
  }
}
function robotStatus(v) {
  return (
    {
      done: "已完成",
      completed: "执行完成",
      delivered: "已发送",
      delivery_pending: "等待发送",
      notified: "已提醒",
      active: "进行中",
      paused: "已暂停",
      cancelled: "已取消",
      pending: "待确认",
      converted: "已加入待办",
      rejected: "已忽略",
      open: "开放中",
      closed: "已关闭",
      merged: "已合并",
      success: "已完成",
      failed: "未完成",
      skipped: "已跳过",
      delivery_failed: "发送失败",
      delivery_uncertain: "发送结果待确认",
    }[v] ||
    stateName[v] ||
    "待查看"
  );
}
function robotInput(label, name, value = "", type = "text", extra = "") {
  return `<label>${esc(label)}<input name="${name}" type="${type}" value="${esc(value)}" ${extra}></label>`;
}
function robotSelect(label, name, options, value) {
  return `<label>${esc(label)}<select name="${name}">${Object.entries(options)
    .map(
      ([k, v]) =>
        `<option value="${esc(k)}" ${String(value) === k ? "selected" : ""}>${esc(v)}</option>`,
    )
    .join("")}</select></label>`;
}
function robotCheck(label, name, checked = false) {
  return `<label class="check"><input type="checkbox" name="${name}" ${checked ? "checked" : ""}>${esc(label)}</label>`;
}
function robotArticle(title, body, actions = "") {
  return `<article class="row robot-record"><div><h3>${esc(title)}</h3>${body}</div>${actions ? `<div class="actions">${actions}</div>` : ""}</article>`;
}
function robotButton(text, attr = "", primary = false) {
  return `<button type="button" ${attr} ${primary ? 'class="primary"' : ""}>${esc(text)}</button>`;
}
async function robotRead(tool, args) {
  try {
    return { value: await nativeCall(tool, args) };
  } catch (error) {
    return { error };
  }
}
function robotFeedback(result, text = "已保存") {
  const warnings = [
    ...(result?.reminder_warnings || []),
    ...(result?.warnings || []),
    result?.reminder_warning,
  ].filter(Boolean);
  notice(warnings.length ? text + "；" + warnings.join("；") : text);
}
function robotAction(el, selector, fn) {
  el.querySelectorAll(selector).forEach(
    (b) =>
      (b.onclick = async () => {
        b.disabled = true;
        try {
          await fn(b);
        } catch (e) {
          notice(e.message);
        } finally {
          b.disabled = false;
        }
      }),
  );
}
function robotNavigate(tool) {
  selectedNativeModule = tool;
  return go("native");
}
function robotBind(el) {
  buttons();
  robotAction(el, "[data-robot-module]", (b) =>
    robotNavigate(b.dataset.robotModule),
  );
  robotAction(el, "[data-robot-draft]", async (b) => {
    const text = b.dataset.robotDraft;
    await go("home");
    const input = document.querySelector("#chat-form [name=text]");
    if (input) {
      input.value = text;
      input.focus();
      input.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  });
}
function robotAsk(text, label = "与助手讨论调整") {
  return robotButton(label, `data-robot-draft="${esc(text)}"`);
}
function robotScheduleLink() {
  return `<p class="policy">自动通知遵循北京时间的作息安排；窗口外的提醒会跳过，不补发。</p><button type="button" data-robot-module="assistant_heartbeat">调整作息与自动安排</button>`;
}
function robotPage(el, tool, body) {
  if (!el.isConnected) throw Error("页面已切换");
  const [title, description] = robotModules[tool];
  el.innerHTML =
    head(title, description) +
    `<div class="robot-toolbar">${robotButton("全部功能", 'data-go="native"')}${robotButton("刷新", 'id="robot-refresh"')}</div>` +
    body;
  robotAction(el, "#robot-refresh", () => robotModulePage(el, tool));
  robotBind(el);
}
async function robotModulePage(el, tool) {
  const handlers = {
    assistant_tasks: robotTasks,
    assistant_memory: robotMemory,
    assistant_subscriptions: robotSubscriptions,
    assistant_mail: robotMail,
    assistant_courses: robotCourses,
    assistant_heartbeat: robotSchedule,
    assistant_weather: robotWeather,
    assistant_weather_alerts: robotAlerts,
    assistant_ai_news: robotNews,
    assistant_github: robotGitHub,
    assistant_proposals: robotProposals,
    assistant_morning: robotBriefing,
    assistant_evening: robotBriefing,
    assistant_server_health: nativeHealthPage,
  };
  try {
    await (handlers[tool] || robotDashboard)(el, tool);
  } catch (e) {
    if (el.isConnected)
      robotPage(el, tool, robotPanel("暂时无法读取", robotError(e)));
  }
}
async function robotDashboard(el) {
  const tools = await nativeTools(),
    available = new Set(tools.map((t) => t.name));
  el.innerHTML =
    head("功能模块", "选择你需要的能力，查看内容并安排助手如何为你服务。") +
    Object.entries(
      Object.groupBy(
        Object.entries(robotModules).filter(([id]) => available.has(id)),
        ([, v]) => v[2],
      ),
    )
      .map(([group, entries]) =>
        robotPanel(
          group,
          `<div class="robot-directory">${entries.map(([id, [title, description]]) => `<button data-robot-module="${id}"><strong>${title}</strong><span>${description}</span><span class="robot-directory-action">打开${title}</span></button>`).join("")}</div>`,
        ),
      )
      .join("");
  robotBind(el);
}
async function robotHome(el) {
  const [status, tasks, proposals, schedule, identity] = await Promise.all([
    api("/native/status"),
    robotRead("assistant_tasks", { action: "list" }),
    robotRead("assistant_proposals", { action: "list" }),
    robotRead("assistant_heartbeat", { action: "get" }),
    ws?.instance_managed
      ? api("/native/identity")
      : Promise.resolve({ name: cfg.name || "助手" }),
  ]);
  if (!el.isConnected) return;
  const list = robotRows(tasks.value)
      .filter((r) => !["done", "cancelled", "notified"].includes(r.status))
      .sort((a, b) =>
        (a.due_at || a.schedule?.at || a.planned_for || "9999").localeCompare(
          b.due_at || b.schedule?.at || b.planned_for || "9999",
        ),
      ),
    suggestions = robotRows(proposals.value),
    window = schedule.value?.effective?.window;
  el.innerHTML =
    head(
      `${identity.name || "助手"}，为你安排日常`,
      "查看接下来的事项，或直接告诉助手你想做什么。",
    ) +
    (!status.running
      ? `<p class="warning">助手当前未运行，自动服务可能无法执行。<button data-go="wechat">查看连接并启动</button></p>`
      : "") +
    robotPanel(
      "接下来要做的事",
      tasks.error
        ? robotError(tasks.error)
        : list.length
          ? list
              .slice(0, 5)
              .map((r) =>
                robotArticle(
                  r.title,
                  `<p class="muted">${r.due_at ? "截止 " + esc(robotTime(r.due_at)) : r.schedule?.at ? "提醒 " + esc(robotTime(r.schedule.at)) : r.schedule?.interval ? esc(robotRepeatLabel(r.schedule.interval)) : "尚未设置截止时间"} · ${esc({ open: "待完成" }[r.status] || robotStatus(r.status))}</p>`,
                  robotButton("查看", 'data-robot-module="assistant_tasks"'),
                ),
              )
              .join("") +
            robotButton("管理全部事项", 'data-robot-module="assistant_tasks"')
          : robotEmpty(
              "暂时没有待办。可以告诉助手“提醒我明天下午提交报告”，也可以手动添加。",
            ) +
            robotButton(
              "添加待办或提醒",
              'data-robot-module="assistant_tasks"',
            ),
    ) +
    (suggestions.length
      ? robotPanel(
          "需要你确认",
          `<p>助手发现了 ${suggestions.length} 条可能需要处理的事项，确认后才会加入待办。</p>${robotButton("查看建议", 'data-robot-module="assistant_proposals"')}`,
        )
      : "") +
    robotPanel(
      "安排你的助手",
      `<div class="robot-quicklinks"><button data-go="assistant"><strong>让它更懂你</strong><span>名称、称呼与回答偏好</span></button><button data-robot-module="assistant_heartbeat"><strong>决定何时联系你</strong><span>${window ? esc(window.enabled ? `${window.start}–${window.end} 允许自动通知` : "今天暂停自动通知") : "查看每周作息和自动服务"}</span></button><button data-go="native"><strong>选择需要的功能</strong><span>邮件、天气、课程和更多</span></button></div>`,
    ) +
    nativeChatForm(identity.name || "助手");
  robotBind(el);
  bindNativeChat(identity.name || "助手");
}
async function robotTasks(el) {
  const rows = nativeNewestFirst(
    robotRows(
      await nativeCall("assistant_tasks", {
        action: "list",
        include_closed: true,
      }),
    ),
    "assistant_tasks",
  );
  robotPage(
    el,
    "assistant_tasks",
    robotPanel(
      "我的事项",
      `<div class="actions">${robotSelect("查看", "task_filter", { active: "进行中", all: "全部", closed: "已完成或已结束" }, "active")}${robotButton("新增事项", 'id="task-new"', true)}</div><div id="task-list"></div><div id="task-editor"></div>`,
    ) +
      robotPanel(
        "提醒如何工作",
        `<p>截止时间表示事情何时要完成；提醒时间表示何时通知你。收到提醒不等于完成待办。</p>${robotScheduleLink()}`,
      ),
  );
  const list = el.querySelector("#task-list"),
    editor = el.querySelector("#task-editor");
  function paint() {
    const filter = el.querySelector("[name=task_filter]").value,
      shown = rows.filter(
        (r) =>
          filter === "all" ||
          (filter === "closed"
            ? ["done", "cancelled", "notified"].includes(r.status)
            : !["done", "cancelled", "notified"].includes(r.status)),
      );
    list.innerHTML = shown.length
      ? shown
          .map((r) =>
            robotArticle(
              r.title,
              `<p>${esc(r.body || "")}</p><p class="muted">${r.due_at ? "截止：" + esc(robotTime(r.due_at)) + " · " : ""}${r.planned_for ? "计划：" + esc(r.planned_for) + " · " : ""}${esc({ open: "待完成" }[r.status] || robotStatus(r.status))}</p>${r.schedule ? `<p class="muted">${r.schedule.at ? "提醒：" + esc(robotTime(r.schedule.at)) : r.schedule.interval ? "重复提醒：" + esc(robotRepeatLabel(r.schedule.interval)) : "按安排提醒"}${r.schedule.enabled === false ? " · 已暂停" : ""}</p>` : ""}`,
              (!["done", "cancelled", "notified"].includes(r.status)
                ? robotButton("编辑", `data-task-edit="${esc(r.id)}"`)
                : "") +
                (!["done", "cancelled", "notified"].includes(r.status)
                  ? robotButton(
                      r.schedule?.interval ? "完成本次" : "完成",
                      `data-task-action="complete" data-id="${esc(r.id)}"`,
                    ) +
                    (r.schedule
                      ? robotButton(
                          r.schedule.enabled === false
                            ? "恢复提醒"
                            : "暂停提醒",
                          `data-task-action="${r.schedule.enabled === false ? "resume" : "pause"}" data-id="${esc(r.id)}"`,
                        )
                      : "") +
                    robotButton(
                      "取消事项",
                      `data-task-action="cancel" data-id="${esc(r.id)}"`,
                    )
                  : ""),
            ),
          )
          .join("")
      : robotEmpty("这里还没有相应的事项。添加要做的事，或设置一条定时提醒。");
    robotAction(list, "[data-task-edit]", (b) =>
      edit(rows.find((r) => r.id === b.dataset.taskEdit)),
    );
    robotAction(list, "[data-task-action]", async (b) => {
      const r = rows.find((r) => r.id === b.dataset.id),
        action = b.dataset.taskAction;
      if (action === "cancel" && !confirm("取消这件事及其后续提醒？")) return;
      robotFeedback(
        await nativeCall("assistant_tasks", {
          action,
          id: r.id,
          revision: r.revision,
          occurrence_only: action === "complete" && !!r.schedule?.interval,
        }),
        action === "complete"
          ? "已完成"
          : action === "cancel"
            ? "事项已取消"
            : "提醒安排已更新",
      );
      await robotTasks(el);
    });
  }
  function edit(r = {}) {
    const existing = !!r.id,
      kind = r.kind || "todo",
      interval = r.schedule?.interval || "";
    editor.innerHTML = `<form id="robot-task-form" class="section"><h3>${existing ? "编辑事项" : "新增事项"}</h3>${existing ? `<p class="muted">${kind === "todo" ? "待办事项" : kind === "automation" ? "自动分析任务" : "定时提醒"}</p>` : robotSelect("我想添加", "kind", { todo: "一件要做的事", reminder: "一条定时提醒" }, kind)}${robotInput("事项标题", "title", r.title || "", "text", 'required maxlength="160"')}<label>${kind === "automation" ? "任务说明" : "补充说明 / 实际提醒内容"}<textarea name="body" maxlength="5000">${esc(r.body || "")}</textarea></label><div class="grid">${robotInput("截止时间（可选）", "due_at", robotLocal(r.due_at), "datetime-local")}${robotInput("计划日期（可选）", "planned_for", r.planned_for || "", "date")}</div>${!existing || kind !== "todo" ? `<div id="task-schedule-fields"><div class="grid">${robotInput("提醒时间（单次提醒填写）", "at", robotLocal(r.schedule?.at), "datetime-local")}${robotSelect("重复方式", "repeat", { once: "仅一次", daily: "每天", weekly: "每周同一天", existing: "保留当前安排" }, interval ? "existing" : "once")}</div>${robotInput("重复提醒的时间", "repeat_time", interval.match(/@(\d\d:\d\d)/)?.[1] || "09:00", "time")}${robotSelect("每周提醒日", "repeat_day", { mon: "周一", tue: "周二", wed: "周三", thu: "周四", fri: "周五", sat: "周六", sun: "周日" }, "mon")}</div>` : ""}${robotCheck("截止前一天和两小时提醒我", "deadline_reminders", existing ? !!r.deadline_reminders : true)}<p class="policy">时间使用北京时间；自动通知受作息限制。</p><div class="actions"><button class="primary">保存事项</button>${robotButton("取消编辑", 'id="task-cancel"')}</div></form>`;
    const taskForm = editor.querySelector("#robot-task-form");
    const drawSchedule = () => {
      const fields = editor.querySelector("#task-schedule-fields");
      if (!fields) return;
      fields.hidden = !existing && taskForm.elements.kind.value !== "reminder";
      const repeat = taskForm.elements.repeat.value;
      taskForm.elements.at.closest("label").hidden = repeat !== "once";
      taskForm.elements.repeat_time.closest("label").hidden = ![
        "daily",
        "weekly",
      ].includes(repeat);
      taskForm.elements.repeat_day.closest("label").hidden =
        repeat !== "weekly";
    };
    taskForm.elements.kind?.addEventListener("change", drawSchedule);
    taskForm.elements.repeat?.addEventListener("change", drawSchedule);
    drawSchedule();
    editor.querySelector("#task-cancel").onclick = () =>
      (editor.innerHTML = "");
    bindForm("robot-task-form", async (b, f) => {
      const args = {
        action: existing ? "update" : "create",
        title: b.title,
        body: b.body,
        deadline_reminders: f.elements.deadline_reminders.checked,
      };
      if (existing) {
        args.id = r.id;
        args.revision = r.revision;
      } else args.kind = b.kind;
      if (b.due_at) args.due_at = robotISO(b.due_at);
      else if (r.due_at) args.due_at = "never";
      if (b.planned_for) args.planned_for = b.planned_for;
      else if (r.planned_for) args.planned_for = "never";
      const scheduled = existing ? kind !== "todo" : b.kind === "reminder";
      if (scheduled) {
        if (!args.body) args.body = b.title;
        if (b.repeat === "existing") {
          if (!existing || !interval) throw Error("请选择实际的重复方式");
        } else if (b.repeat === "once") {
          if (!b.at) throw Error("请填写提醒时间");
          args.at = robotISO(b.at);
        } else {
          if (!b.repeat_time) throw Error("请选择重复提醒的时间");
          args.interval =
            b.repeat === "daily"
              ? `24h|daily@${b.repeat_time}`
              : `168h|weekly:${b.repeat_day}@${b.repeat_time}`;
        }
      }
      robotFeedback(await nativeCall("assistant_tasks", args), "事项已保存");
      await robotTasks(el);
    });
    editor.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  el.querySelector("[name=task_filter]").onchange = paint;
  el.querySelector("#task-new").onclick = () => edit();
  paint();
}
function robotRepeatLabel(v) {
  const daily = v.match(/^24h\|daily@(\d\d:\d\d)$/),
    weekly = v.match(/^168h\|weekly:([a-z,]+)@(\d\d:\d\d)$/);
  if (daily) return "每天 " + daily[1];
  if (weekly)
    return (
      "每周 " +
      weekly[1]
        .split(",")
        .map(
          (x) =>
            ({
              mon: "一",
              tue: "二",
              wed: "三",
              thu: "四",
              fri: "五",
              sat: "六",
              sun: "日",
            })[x] || x,
        )
        .join("、") +
      " " +
      weekly[2]
    );
  return "自定义周期（编辑时可保留）";
}
async function robotMemory(el) {
  const rows = nativeNewestFirst(
    robotRows(
      await nativeCall("assistant_memory", {
        action: "list",
        include_inactive: true,
      }),
    ),
    "assistant_memory",
  );
  robotPage(
    el,
    "assistant_memory",
    robotPanel(
      "助手记住的事",
      `<div class="grid">${robotInput("搜索主题或内容", "memory_search", "", "search", 'placeholder="例如：回答偏好、出行安排"')}${robotSelect("查看范围", "memory_filter", { current: "当前与未来生效", all: "全部", active: "正在生效", future: "未来生效", expired: "已过期", archived: "已归档" }, "current")}</div>${robotButton("添加记忆", 'id="memory-new"', true)}<div id="robot-memory-list"></div><div id="robot-memory-editor"></div>`,
    ) +
      robotPanel(
        "由你决定记住什么",
        "<p>长期偏好可以一直保留，临时安排可以设置有效期。归档后，助手不再把它作为当前偏好使用。</p>",
      ),
  );
  const editor = el.querySelector("#robot-memory-editor");
  function edit(r = {}) {
    editor.innerHTML = `<form id="robot-memory-form" class="section"><h3>${r.id ? "纠正这条记忆" : "添加一条记忆"}</h3>${robotInput("主题", "subject", r.subject || "", "text", 'required maxlength="160"')}<label>希望助手记住什么<textarea name="body" required maxlength="5000" placeholder="例如：回复先给结论，再补充必要说明">${esc(r.body || "")}</textarea></label>${robotSelect("保留多久", "kind", { long: "长期保留", short: "在一段时间内生效" }, r.kind || "long")}<div class="grid">${robotInput("开始生效（临时记忆必填）", "starts_at", robotLocal(r.starts_at), "datetime-local")}${robotInput("不再适用的时间（临时记忆必填）", "expires_at", robotLocal(r.expires_at), "datetime-local")}</div><div class="actions"><button class="primary">保存记忆</button>${robotButton("取消编辑", 'id="memory-cancel-edit"')}</div></form>`;
    editor.querySelector("#memory-cancel-edit").onclick = () =>
      (editor.innerHTML = "");
    bindForm("robot-memory-form", async (b) => {
      if (b.kind === "short" && (!b.starts_at || !b.expires_at))
        throw Error("请填写临时记忆的生效与结束时间");
      if (b.starts_at && b.expires_at && b.starts_at >= b.expires_at)
        throw Error("结束时间应晚于生效时间");
      const args = {
        action: r.id ? "update" : "create",
        subject: b.subject,
        body: b.body,
        kind: b.kind,
      };
      if (r.id) {
        args.id = r.id;
        args.revision = r.revision;
      }
      for (const k of ["starts_at", "expires_at"]) {
        if (b[k]) args[k] = robotISO(b[k]);
      }
      await nativeCall("assistant_memory", args);
      notice("记忆已保存");
      await robotMemory(el);
    });
    editor.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  function paint() {
    const q = el
        .querySelector("[name=memory_search]")
        .value.trim()
        .toLowerCase(),
      filter = el.querySelector("[name=memory_filter]").value,
      shown = rows.filter(
        (r) =>
          (filter === "all" ||
            (filter === "current"
              ? ["active", "future"].includes(r.status)
              : r.status === filter)) &&
          `${r.subject} ${r.body}`.toLowerCase().includes(q),
      ),
      list = el.querySelector("#robot-memory-list");
    list.innerHTML = shown.length
      ? shown
          .map((r) =>
            robotArticle(
              r.subject,
              `<p>${esc(r.body)}</p><p class="muted">${esc(memoryStatusNames[r.status] || "待查看")} · ${r.kind === "short" ? "临时安排" : "长期记忆"}${r.expires_at ? " · 有效至 " + esc(robotTime(r.expires_at)) : ""}</p>${r.status === "future" ? `<p class="muted">从 ${esc(robotTime(r.starts_at))} 开始适用</p>` : ""}`,
              ["active", "future"].includes(r.status)
                ? robotButton("纠正", `data-memory-edit="${esc(r.id)}"`) +
                    robotButton("归档", `data-memory-archive="${esc(r.id)}"`)
                : "",
            ),
          )
          .join("")
      : robotEmpty(
          "没有符合条件的记忆。可以添加一条回答偏好，让助手逐渐了解你。",
        );
    robotAction(list, "[data-memory-edit]", (b) =>
      edit(rows.find((r) => r.id === b.dataset.memoryEdit)),
    );
    robotAction(list, "[data-memory-archive]", async (b) => {
      const r = rows.find((r) => r.id === b.dataset.memoryArchive);
      if (!confirm("归档后，助手将不再使用这条记忆。继续？")) return;
      await nativeCall("assistant_memory", {
        action: "archive",
        id: r.id,
        revision: r.revision,
      });
      notice("记忆已归档");
      await robotMemory(el);
    });
  }
  el.querySelector("#memory-new").onclick = () => edit();
  el.querySelector("[name=memory_search]").oninput = paint;
  el.querySelector("[name=memory_filter]").onchange = paint;
  paint();
}
async function robotSubscriptions(el) {
  const [rows, defaults] = await Promise.all([
    nativeCall("assistant_subscriptions", { action: "list" }),
    nativeCall("assistant_subscriptions", { action: "get_default" }),
  ]);
  const sorted = robotRows(rows)
    .slice()
    .sort((a, b) =>
      (a.expires_on || a.expires_at || "").localeCompare(
        b.expires_on || b.expires_at || "",
      ),
    );
  robotPage(
    el,
    "assistant_subscriptions",
    robotPanel(
      "我的到期事项",
      robotButton("添加到期事项", 'id="expiry-new"', true) +
        `<div id="expiry-list">${
          sorted
            .map((r, i) => {
              const expiry = r.expires_on || r.expires_at,
                day = String(expiry).slice(0, 10),
                diff = Math.ceil(
                  (Date.parse(day + "T00:00:00+08:00") -
                    Date.parse(robotDay() + "T00:00:00+08:00")) /
                    86400000,
                );
              return robotArticle(
                r.name,
                `<p>${esc(r.expires_on || robotTime(r.expires_at))} 到期 · ${diff < 0 ? "已过期" : diff === 0 ? "今天到期" : `还剩 ${diff} 天`}</p><p class="muted">${r.reminder_on ? "在 " + esc(r.reminder_on) + " 提醒" : "提前 " + (r.reminder_days ?? defaults.default_reminder_days ?? 7) + " 天提醒"} · ${{ manual: "手动续期", auto: "自动续期", unknown: "续期方式未确定" }[r.renewal_mode] || "续期方式未确定"}</p>`,
                robotButton("编辑", `data-expiry-edit="${i}"`) +
                  robotButton("登记续期", `data-expiry-renew="${i}"`) +
                  robotButton("归档", `data-expiry-archive="${i}"`),
              );
            })
            .join("") ||
          robotEmpty("添加会员、域名、证书或优惠券，让助手在到期前提醒你。")
        }</div><div id="expiry-editor"></div>`,
    ) +
      robotPanel(
        "新事项的默认提醒",
        `<form id="expiry-default">${robotInput("默认提前多少天提醒", "days", defaults.default_reminder_days ?? 7, "number", 'min="0" max="3650" required')}<button>保存默认天数</button><p class="policy">只影响以后新增的事项；登记续期不会替你付款或修改服务商订阅。</p></form>${robotScheduleLink()}`,
      ),
  );
  function edit(r = {}, renew = false) {
    const editor = el.querySelector("#expiry-editor");
    editor.innerHTML = `<form id="expiry-form" class="section"><h3>${renew ? "登记新的到期时间" : r.id ? "编辑到期事项" : "添加到期事项"}</h3>${robotInput("名称", "name", r.name || "", "text", 'required maxlength="160"')}${robotInput("到期日期", "expires_on", r.expires_on || "", "date")}${robotInput("精确到期时间（与日期二选一）", "expires_at", robotLocal(r.expires_at), "datetime-local")}${robotSelect("续期方式", "renewal_mode", { unknown: "尚未确定", manual: "我会手动续期", auto: "服务商自动续期" }, r.renewal_mode || "unknown")}<div class="grid">${robotInput("提前提醒天数（与指定日期二选一）", "reminder_days", r.reminder_on ? "" : (r.reminder_days ?? defaults.default_reminder_days ?? 7), "number", 'min="0" max="3650"')}${robotInput("指定提醒日期", "reminder_on", r.reminder_on || "", "date")}</div><div class="actions"><button class="primary">${renew ? "保存续期记录" : "保存事项"}</button>${robotButton("取消编辑", 'id="expiry-cancel"')}</div></form>`;
    editor.querySelector("#expiry-cancel").onclick = () =>
      (editor.innerHTML = "");
    bindForm("expiry-form", async (b) => {
      if (!!b.expires_on === !!b.expires_at)
        throw Error("到期日期和精确时间请选择一项填写");
      if (b.reminder_on && b.reminder_days !== "")
        throw Error("请只填写提前天数或指定提醒日期");
      const args = {
        action: r.id ? (renew ? "renew" : "update") : "create",
        name: b.name,
        renewal_mode: b.renewal_mode,
      };
      if (r.id) {
        args.id = r.id;
        args.revision = r.revision;
      }
      if (b.expires_on) args.expires_on = b.expires_on;
      else args.expires_at = robotISO(b.expires_at);
      if (b.reminder_on) args.reminder_on = b.reminder_on;
      else if (b.reminder_days !== "")
        args.reminder_days = Number(b.reminder_days);
      const result = await nativeCall("assistant_subscriptions", args);
      robotFeedback(
        result,
        result.reminder_date
          ? "已保存；提醒日期 " + result.reminder_date
          : "到期事项已保存",
      );
      await robotSubscriptions(el);
    });
    editor.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  el.querySelector("#expiry-new").onclick = () => edit();
  robotAction(el, "[data-expiry-edit]", (b) =>
    edit(sorted[+b.dataset.expiryEdit]),
  );
  robotAction(el, "[data-expiry-renew]", (b) =>
    edit(sorted[+b.dataset.expiryRenew], true),
  );
  robotAction(el, "[data-expiry-archive]", async (b) => {
    const r = sorted[+b.dataset.expiryArchive];
    if (!confirm("归档后停止此事项的到期提醒？")) return;
    await nativeCall("assistant_subscriptions", {
      action: "archive",
      id: r.id,
      revision: r.revision,
    });
    await robotSubscriptions(el);
  });
  bindForm("expiry-default", async (b) => {
    await nativeCall("assistant_subscriptions", {
      action: "set_default",
      reminder_days: Number(b.days),
    });
    notice("新增事项的默认提醒已更新");
    await robotSubscriptions(el);
  });
}
async function robotMail(el) {
  const status = await robotRead("assistant_mail", { action: "status" }),
    state = status.value || {},
    sources = robotRows(state.sources),
    health = robotRows(state.health);
  robotPage(
    el,
    "assistant_mail",
    robotPanel(
      "最近邮件",
      `<form id="mail-query" class="actions">${robotInput("搜索最近邮件", "query", "", "search", 'placeholder="主题或发件人"')}<button>搜索</button>${robotButton("查看最近邮件", 'id="mail-recent"')}${robotButton("未主动通知的邮件", 'id="mail-silent"')}</form><p class="policy">搜索范围为近期邮件及已同步内容，不是完整邮箱归档。</p><div id="mail-results">${robotEmpty("点击“查看最近邮件”读取内容，不会发送邮件。")}</div><div id="mail-reader"></div>`,
    ) +
      robotPanel(
        "我的邮箱",
        (status.error ? robotError(status.error) : "") +
          sources
            .map((a) => {
              const h = health.find((x) => x.account === a.id);
              return robotArticle(
                a.address ? nativeMask(a.address) : nativeMailName(a.id),
                `<p>${h?.error ? "最近同步未成功，请检查授权或连接" : "最近成功同步：" + esc(robotTime(h?.last_success))}</p>`,
              );
            })
            .join("") ||
          robotEmpty(
            "连接邮箱后，助手可以帮你阅读新邮件并发现需要处理的事项。",
          ),
      ) +
      robotPanel(
        "邮箱设置",
        `<div class="actions">${ws?.instance_managed ? robotButton("管理邮箱连接", 'id="mail-connect"') : ""}${robotButton("检查邮箱授权", 'id="mail-auth"')}${robotButton("自动检查与通知时间", 'data-robot-module="assistant_heartbeat"')}</div><p class="policy">授权检查只检查登录，不发送邮件，也不修改邮件已读状态。当前提供阅读和整理功能。</p>`,
      ),
  );
  let request = 0,
    lastArgs = { action: "list", limit: 20 },
    offset = 0;
  async function load(args, append = false) {
    const ticket = ++request,
      out = el.querySelector("#mail-results");
    if (!append) out.innerHTML = '<p class="loading">正在读取邮件…</p>';
    try {
      const data = await nativeCall("assistant_mail", args);
      if (ticket !== request) return;
      const rows = robotRows(data.messages || data.items || data),
        html = rows
          .map((entry, i) => {
            const r = entry.message || entry;
            return robotArticle(
              r.subject || "无主题",
              `<p class="muted">${esc(r.from || "发件人未知")} · ${esc(robotTime(r.received_at || r.date))}</p><p>${esc(r.snippet || entry.summary || "")}</p>`,
              robotButton("阅读", `data-mail-read="${esc(r.id)}"`),
            );
          })
          .join("");
      out.querySelector("#mail-more")?.remove();
      if (append) out.insertAdjacentHTML("beforeend", html);
      else out.innerHTML = html || robotEmpty("没有查到相应邮件。");
      if (robotRows(data.accounts).some((a) => !a.ok))
        out.insertAdjacentHTML(
          "afterbegin",
          '<p class="warning">部分邮箱暂时读取失败，当前列表可能不完整。</p>',
        );
      offset = data.next_offset || 0;
      lastArgs = args;
      if (data.has_more)
        out.insertAdjacentHTML(
          "beforeend",
          robotButton("加载更多", 'id="mail-more"'),
        );
      robotAction(out, "#mail-more", () => load({ ...lastArgs, offset }, true));
      robotAction(out, "[data-mail-read]", (b) => read(b.dataset.mailRead));
    } catch (e) {
      if (ticket === request) out.innerHTML = robotError(e);
    }
  }
  let readTicket = 0;
  async function read(id, next = 0) {
    const ticket = ++readTicket,
      reader = el.querySelector("#mail-reader");
    try {
      const data = await nativeCall("assistant_mail", {
        action: "read",
        id,
        body_offset: next,
      });
      if (ticket !== readTicket) return;
      if (!next)
        reader.innerHTML = `<section class="section"><h3>${esc(data.message?.subject || "邮件正文")}</h3><p class="muted">${esc(data.message?.from || "")}</p><div class="robot-prose" id="mail-body"></div><div id="mail-read-controls"></div></section>`;
      reader
        .querySelector("#mail-body")
        .append(document.createTextNode(data.body || "暂无可读正文"));
      const controls = reader.querySelector("#mail-read-controls");
      controls.innerHTML =
        (data.source_truncated
          ? '<p class="warning">来源正文有截断，当前内容并不完整。</p>'
          : "") +
        (data.has_more
          ? robotButton("继续读取正文", 'id="mail-body-more"')
          : "") +
        (robotRows(data.attachments).length
          ? '<p class="policy">邮件含附件，当前页面未解析附件内容。</p>'
          : "");
      robotAction(controls, "#mail-body-more", () =>
        read(id, data.next_body_offset),
      );
      if (!next) reader.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (e) {
      notice(e.message);
    }
  }
  bindForm("mail-query", (b) =>
    load({ action: b.query ? "search" : "list", query: b.query, limit: 20 }),
  );
  el.querySelector("#mail-recent").onclick = () =>
    load({ action: "list", limit: 20 });
  el.querySelector("#mail-silent").onclick = () =>
    load({ action: "silent", limit: 20 });
  el.querySelector("#mail-connect")?.addEventListener("click", () =>
    nativeMailSetup(el),
  );
  robotAction(el, "#mail-auth", async () => {
    const results = await api("/native/mail-test", {
      method: "POST",
      body: {},
    });
    el.querySelector("#mail-auth").parentElement.insertAdjacentHTML(
      "afterend",
      `<div role="status">${results.map((r) => `<p>${esc(nativeMailName(r.id || r.account || ""))}：${r.authenticated ? "授权有效" : "授权未通过，请更新连接"}</p>`).join("")}</div>`,
    );
  });
  if (sources.length) await load({ action: "list", limit: 20 });
}
function robotTriggerText(t) {
  if (t.mode === "daily") return t.clock ? `每天 ${t.clock}` : "每天随作息开始";
  if (t.mode === "interval")
    return `每 ${Math.round(t.interval_seconds / 60)} 分钟`;
  if (t.mode === "course") return `每节课前 ${t.lead_minutes || 0} 分钟`;
  if (t.mode === "subscription") return "到期提醒日的作息开始时";
  if (t.mode === "once") return robotTime(t.at);
  return "按自动安排执行";
}
async function robotSchedule(el) {
  const d = await nativeCall("assistant_heartbeat", { action: "get" }),
    c = d.config,
    modules = robotRows(d.modules),
    triggers = Object.values(c.triggers || {}),
    names = Object.fromEntries(modules.map((m) => [m.id, m.name])),
    w = d.effective?.window;
  robotPage(
    el,
    "assistant_heartbeat",
    robotPanel(
      "每周允许通知的时间",
      `<p>主动对话全天可用。这里决定助手什么时候可以主动发消息；时间均为北京时间。</p>${w ? `<p class="robot-summary">今天${w.enabled ? "允许通知：" + esc(w.start) + "–" + esc(w.end) : "暂停自动通知"}${d.effective.source === "date_override" ? "（使用指定日期安排）" : ""}</p>` : ""}<form id="routine-form">${Object.entries(
        days,
      )
        .map(([key, label]) => {
          const v = c.weekly_schedule?.[key] || {
            enabled: false,
            start: "08:00",
            end: "23:00",
          };
          return `<div class="week-row">${robotCheck(label, key + "_enabled", v.enabled)}${robotInput("开始", key + "_start", v.start || "08:00", "time", "required")}${robotInput("结束", key + "_end", v.end || "23:00", "text", 'required pattern="([01][0-9]|2[0-3]):[0-5][0-9]|24:00" placeholder="23:00 或 24:00"')}</div>`;
        })
        .join("")}<button class="primary">保存每周作息</button></form>`,
    ) +
      robotPanel(
        "自动服务安排",
        `<p class="muted">固定时间在允许通知的时段之外时会被跳过，不会推迟补发。</p>${triggers.map((t, i) => robotArticle(t.title || "自动安排", `<p>${esc(robotTriggerText(t))} · ${t.enabled ? "已启用" : "已暂停"}</p><p class="muted">${esc(t.modules.map((m) => names[m] || "自动服务").join("、"))}${t.weekdays?.length ? " · " + esc(t.weekdays.map((x) => days[x]).join("、")) : ""}</p>`, robotButton("调整", `data-trigger-edit="${i}"`) + robotButton(t.enabled ? "暂停" : "启用", `data-trigger-toggle="${i}"`))).join("")}${robotButton("添加自动安排", 'id="trigger-new"')}<div id="trigger-editor"></div>`,
      ) +
      robotPanel(
        "各项服务开关",
        modules
          .map((m, i) =>
            robotArticle(
              m.name,
              `<p class="muted">${c.module_enabled?.[m.id] ? "允许按自动安排执行" : "已暂停自动执行"}</p>`,
              robotButton(
                c.module_enabled?.[m.id] ? "停用" : "启用",
                `data-module-toggle="${i}"`,
              ),
            ),
          )
          .join(""),
      ) +
      robotPanel(
        "某一天临时不同",
        `<form id="routine-exception"><div class="grid">${robotInput("日期", "date", "", "date", `required min="${robotDay()}"`)}${robotCheck("当天允许自动通知", "enabled", true)}${robotInput("开始时间", "start", "08:00", "time", "required")}${robotInput("结束时间", "end", "23:00", "text", 'required pattern="([01][0-9]|2[0-3]):[0-5][0-9]|24:00"')}</div><button>保存当天安排</button></form>${Object.entries(
          c.date_overrides || {},
        )
          .map(([date, o]) =>
            robotArticle(
              date,
              `<p>${o.enabled === false ? "全天暂停自动通知" : `${esc(o.start || "沿用每周开始时间")} — ${esc(o.end || "沿用每周结束时间")}`}</p>`,
              robotButton("恢复默认", `data-override-remove="${esc(date)}"`),
            ),
          )
          .join("")}`,
      ),
  );
  const mutate = async (args, text) => {
    await nativeCall("assistant_heartbeat", { revision: c.revision, ...args });
    notice(text);
    await robotSchedule(el);
  };
  bindForm("routine-form", async (b, f) => {
    const weekly_schedule = Object.fromEntries(
      Object.keys(days).map((k) => [
        k,
        {
          enabled: f.elements[k + "_enabled"].checked,
          start: b[k + "_start"],
          end: b[k + "_end"],
        },
      ]),
    );
    await mutate({ action: "set_weekly", weekly_schedule }, "每周作息已更新");
  });
  bindForm("routine-exception", async (b, f) =>
    mutate(
      {
        action: "set_date_override",
        date: b.date,
        enabled: f.elements.enabled.checked,
        start: b.start,
        end: b.end,
      },
      "当天作息已更新",
    ),
  );
  robotAction(el, "[data-override-remove]", async (b) => {
    if (confirm("移除这一天的全部例外，恢复每周默认安排？"))
      await mutate(
        { action: "remove_date_override", date: b.dataset.overrideRemove },
        "已恢复默认安排",
      );
  });
  robotAction(el, "[data-trigger-toggle]", (b) => {
    const t = triggers[+b.dataset.triggerToggle];
    return mutate(
      { action: "set_trigger", trigger_id: t.id, enabled: !t.enabled },
      "自动安排已更新",
    );
  });
  robotAction(el, "[data-module-toggle]", (b) => {
    const m = modules[+b.dataset.moduleToggle];
    return mutate(
      {
        action: "set_module_enabled",
        module_id: m.id,
        enabled: !c.module_enabled?.[m.id],
      },
      "服务开关已更新",
    );
  });
  function edit(t = {}) {
    const existing = !!t.id,
      editor = el.querySelector("#trigger-editor");
    editor.innerHTML = `<form id="trigger-form" class="section"><h3>${existing ? "调整自动安排" : "添加自动安排"}</h3>${robotInput("安排名称", "title", t.title || "", "text", 'required maxlength="100"')}${existing ? `<p>${esc(robotTriggerText(t))}</p>` : robotSelect("执行方式", "mode", { daily: "每天固定时间", interval: "间隔检查", once: "只执行一次", course: "跟随实际课程提醒", subscription: "跟随到期日期提醒" }, "daily")}<div class="grid">${!existing || t.mode === "daily" ? robotInput("每天的时间（留空随作息开始）", "clock", t.clock || "", "time") : ""}${!existing || t.mode === "interval" ? robotInput("检查间隔（分钟）", "minutes", (t.interval_seconds || 900) / 60, "number", 'min="1" max="1440"') : ""}${!existing || t.mode === "once" ? robotInput("执行时间", "at", robotLocal(t.at), "datetime-local") : ""}${!existing || t.mode === "course" ? robotInput("课前提前多少分钟", "lead", t.lead_minutes || 10, "number", 'min="1" max="180"') : ""}</div><fieldset><legend>执行哪些服务</legend>${modules.map((m) => `<div data-schedule-module="${esc(m.id)}">${robotCheck(m.name, "module_" + m.id, !!t.modules?.includes(m.id))}</div>`).join("")}</fieldset>${Object.keys(t.weekday_modules || {}).length ? '<p class="policy">这里调整默认服务组合，既有的各星期专属组合会保留。</p>' : ""}${robotCheck("启用这项安排", "enabled", existing ? t.enabled : true)}<p class="policy">每天固定时间留空时随作息开始。固定时刻超出作息会跳过；课程和到期提醒根据业务日期自动计算时间。</p><div class="actions"><button class="primary">保存安排</button>${robotButton("取消编辑", 'id="trigger-cancel"')}${existing ? robotButton("删除这项安排", 'id="trigger-delete"') : ""}</div></form>`;
    const form = editor.querySelector("#trigger-form");
    const draw = () => {
      const mode = existing ? t.mode : form.elements.mode.value;
      for (const [name, allowed] of Object.entries({
        clock: ["daily"],
        minutes: ["interval"],
        at: ["once"],
        lead: ["course"],
      })) {
        const field = form.elements[name];
        if (!field) continue;
        field.closest("label").hidden = !allowed.includes(mode);
        field.disabled = !allowed.includes(mode);
        field.required = allowed.includes(mode) && name !== "clock";
      }
      for (const m of modules) {
        const row = form.querySelector(`[data-schedule-module="${m.id}"]`),
          field = form.elements["module_" + m.id],
          allowed =
            mode === "course"
              ? m.id === "course_reminder"
              : mode === "subscription"
                ? m.id === "subscription_reminder"
                : !["course_reminder", "subscription_reminder"].includes(m.id);
        row.hidden = !allowed;
        field.disabled = !allowed;
        if (!allowed) field.checked = false;
        else if (["course", "subscription"].includes(mode))
          field.checked = true;
      }
    };
    form.elements.mode?.addEventListener("change", draw);
    draw();
    editor.querySelector("#trigger-cancel").onclick = () =>
      (editor.innerHTML = "");
    robotAction(editor, "#trigger-delete", async () => {
      if (confirm("删除这项自动安排？业务记录会保留。"))
        await mutate(
          { action: "remove_trigger", trigger_id: t.id },
          "自动安排已删除",
        );
    });
    bindForm("trigger-form", async (b, f) => {
      const mode = existing ? t.mode : b.mode,
        args = {
          action: "set_trigger",
          trigger_id: t.id || "web-" + crypto.randomUUID().slice(0, 8),
          title: b.title,
          enabled: f.elements.enabled.checked,
        };
      const selected = modules
        .filter(
          (m) =>
            !f.elements["module_" + m.id].disabled &&
            f.elements["module_" + m.id].checked,
        )
        .map((m) => m.id);
      if (!selected.length) throw Error("请至少选择一项服务");
      if (!existing) {
        args.mode = mode;
        args.modules = selected;
      } else if (
        [...selected].sort().join(",") !== [...t.modules].sort().join(",")
      )
        args.modules = selected;
      if (mode === "daily") {
        if (b.clock) args.clock = b.clock;
        else if (t.clock)
          throw Error("现有固定时间不能直接留空；可新建随作息开始的安排");
      }
      if (mode === "interval") args.interval_seconds = Number(b.minutes) * 60;
      if (mode === "course") args.lead_minutes = Number(b.lead);
      if (mode === "once") args.at = robotISO(b.at);
      await mutate(args, "自动安排已保存");
    });
    editor.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  el.querySelector("#trigger-new").onclick = () => edit();
  robotAction(el, "[data-trigger-edit]", (b) =>
    edit(triggers[+b.dataset.triggerEdit]),
  );
}
function robotNumberList(text, max = 100) {
  if (!text.trim()) return [];
  const values = new Set();
  for (const part of text.replaceAll("，", ",").split(",")) {
    const match = part.trim().match(/^(\d+)(?:\s*-\s*(\d+))?$/);
    if (!match) throw Error("请使用数字、逗号或范围，例如 1-8,10");
    const first = +match[1],
      last = +(match[2] || match[1]);
    if (first < 1 || last > max || first > last)
      throw Error("填写的范围不正确");
    for (let i = first; i <= last; i++) values.add(i);
  }
  return [...values].sort((a, b) => a - b);
}
function robotAcademic(state, heartbeat) {
  if (!state?.configured)
    return robotPanel(
      "校历与上学安排",
      robotEmpty(
        state === null
          ? "校历暂时无法读取，请稍后刷新。"
          : "尚未导入校历。导入后可按学期、节假日和调休判断教学日。",
      ),
    );
  const c = state.calendar,
    today = state.today;
  const config = heartbeat?.config || heartbeat;
  const reminders = Object.values(config?.triggers || {}).filter(
    (t) => t.enabled && t.modules?.includes("schoolday_reminder"),
  );
  const active = config?.module_enabled?.schoolday_reminder && reminders.length;
  const pending = Object.entries(c.holiday_years || {})
    .filter(([, ok]) => !ok)
    .map(([y]) => y);
  return robotPanel(
    "校历与上学安排",
    `<p><strong>${esc(today.date)} · ${today.known ? (today.teaching ? "今天是教学日" : "今天休息") : "日期尚未覆盖"}</strong></p><p>${esc(today.reason)}</p>
    <p class="muted">${esc(c.school)} · ${c.terms.map((t) => esc(t.name) + " " + esc(t.start) + " 至 " + esc(t.end)).join("；")}</p>
    <div class="robot-notice"><strong>UU 远程电脑提醒${heartbeat ? (active ? " · 已开启" : " · 已暂停") : " · 暂时无法读取状态"}</strong><p>${active ? reminders.map((t) => esc(t.clock || "按作息触发")).join("、") + "（北京时间），仅教学日提醒你用 UU 远程打开宿舍里的 Windows 电脑。" : "可在作息与自动安排中设置教学日提醒。"}放假不提醒，调休补课日照常提醒。</p>${robotScheduleLink()}</div>
    <details class="robot-details"><summary>接下来两周的安排</summary>${state.days.map((d) => robotArticle(d.date + " · " + (d.known ? (d.teaching ? "上学" : "休息") : "待补充校历"), `<p>${esc(d.reason)}${d.class_date && d.class_date !== d.date ? " · 按 " + esc(d.class_date) + " 的课程" : ""}${d.provisional ? " · 调休安排待核实" : ""}</p>`)).join("")}</details>
    ${pending.length ? `<p class="robot-notice">${esc(pending.join("、"))} 年完整放假调休安排尚未核实，暂按校历与已知法定假日判断。官方通知更新后会自动同步。</p>` : ""}
    <p class="muted">在校时段按教学日连续安排推断：返校以 17:00、返家以 19:00 为分界，临时行程优先。调休补课日采用对应课程日的作息；尚未明确对应日期时采用周一作息。</p>
    <details class="robot-details"><summary>校历来源与更新</summary><p>每天检查学校和政府的官方通知。${state.sync?.checked_at ? "最近检查：" + esc(robotTime(state.sync.checked_at)) + "。" : ""}${state.sync?.errors?.length ? "部分来源暂时无法访问，继续使用最近有效的安排。" : ""}</p>${(c.sources || []).map((s) => `<p>${robotLink(s.url, s.title)}</p>`).join("")}</details>`,
  );
}

async function robotCourses(el) {
  const [state, academic, heartbeat] = await Promise.all([
    nativeCall("assistant_courses", { action: "status" }),
    nativeCall("assistant_academic", { action: "status" }).catch(() => null),
    nativeCall("assistant_heartbeat", { action: "get" }).catch(() => null),
  ]);
  const c = state.config || {
      name: "我的课表",
      first_week_monday: "",
      total_weeks: 20,
      timezone: "Asia/Shanghai",
      periods: [],
      courses: [],
      exceptions: [],
    },
    revision = c.revision || 0;
  robotPage(
    el,
    "assistant_courses",
    robotAcademic(academic, heartbeat) +
      robotPanel(
        "查看课程",
        `<form id="course-date" class="actions">${robotInput("选择日期", "date", robotDay(), "date", "required")}<button>查看这一天</button>${robotButton("编辑课表", 'id="course-edit"', true)}</form><div id="course-day"></div>`,
      ) +
      robotPanel(
        "学期与课程",
        `<p>${esc(c.name)}${c.first_week_monday ? " · 第一周周一 " + esc(c.first_week_monday) + " · 共 " + c.total_weeks + " 周" : ""}</p>${
          robotRows(c.courses)
            .map((r, i) =>
              robotArticle(
                r.title,
                `<p>${["", "周一", "周二", "周三", "周四", "周五", "周六", "周日"][r.weekday]} · 第 ${esc(r.periods.join("、"))} 节 · ${esc(r.room || "教室未填")}</p><p class="muted">${r.weeks?.length ? "第 " + esc(r.weeks.join("、")) + " 周" : "每个教学周"}${r.teacher ? " · " + esc(r.teacher) : ""}</p>`,
                robotButton("停课或调课", `data-course-exception="${i}"`),
              ),
            )
            .join("") ||
          robotEmpty(
            "还没有课程。先填写学期、节次和课程，助手才能按课表提供提醒。",
          )
        }<div id="course-exception-editor"></div>${robotRows(c.exceptions)
          .map((x, i) =>
            robotArticle(
              (c.courses.find((r) => r.id === x.course_id)?.title || "课程") +
                " · " +
                x.date,
              `<p>${x.cancelled ? "本次停课" : (x.move_to ? "改至 " + esc(x.move_to) + " " : "") + (x.start ? esc(x.start) + "–" + esc(x.end) + " " : "") + (x.room ? "教室 " + esc(x.room) : "")}</p>`,
              robotButton("恢复原安排", `data-course-restore="${i}"`),
            ),
          )
          .join("")}`,
      ) +
      `<div id="course-editor"></div>` +
      robotPanel("课前提醒", robotScheduleLink()),
  );
  let dayRequest = 0;
  async function loadDay(date) {
    const ticket = ++dayRequest,
      out = el.querySelector("#course-day");
    out.innerHTML = '<p class="loading">正在读取课程…</p>';
    try {
      const d = await nativeCall("assistant_courses", {
        action: "query",
        date,
      });
      if (ticket !== dayRequest) return;
      out.innerHTML = !d.configured
        ? robotEmpty("先添加课表，即可在这里查看当天安排。")
        : !d.in_term
          ? robotEmpty("所选日期不在当前教学学期内。")
          : `<p class="muted">${esc(date)}${d.week > 0 ? " · 第 " + d.week + " 教学周" : ""}${d.academic ? " · " + esc(d.academic.reason) + (d.academic.class_date && d.academic.class_date !== date ? "（按 " + esc(d.academic.class_date) + " 的课表）" : "") : ""}</p>` +
            (robotRows(d.courses)
              .map((r) =>
                robotArticle(
                  r.title,
                  `<p>${esc(robotTime(r.starts_at))} 至 ${esc(robotTime(r.ends_at))}</p><p>${esc(r.room || "教室未填")}${r.teacher ? " · " + esc(r.teacher) : ""}</p>`,
                ),
              )
              .join("") || robotEmpty("这一天没有课程。"));
    } catch (e) {
      if (ticket === dayRequest) out.innerHTML = robotError(e);
    }
  }
  bindForm("course-date", (b) => loadDay(b.date));
  await loadDay(robotDay());
  robotAction(el, "[data-course-restore]", async (b) => {
    const x = c.exceptions[+b.dataset.courseRestore];
    await nativeCall("assistant_courses", {
      action: "remove_exception",
      revision,
      course_id: x.course_id,
      date: x.date,
    });
    notice("已恢复原课程安排");
    await robotCourses(el);
  });
  robotAction(el, "[data-course-exception]", (b) => {
    const r = c.courses[+b.dataset.courseException],
      editor = el.querySelector("#course-exception-editor");
    editor.innerHTML = `<form id="course-exception-form" class="section"><h3>调整「${esc(r.title)}」的一次课程</h3>${robotInput("原上课日期", "date", robotDay(), "date", "required")}${robotCheck("这一次停课", "cancelled")}<div class="grid">${robotInput("改到哪一天（可选）", "move_to", "", "date")}${robotInput("更换教室（可选）", "room", "", "text")}${robotInput("新开始时间（可选）", "start", "", "time")}${robotInput("新结束时间（可选）", "end", "", "time")}</div><button class="primary">保存本次调整</button></form>`;
    bindForm("course-exception-form", async (v, f) => {
      if (!!v.start !== !!v.end)
        throw Error("修改上课时间时请同时填写开始与结束时间");
      const exception = {
        course_id: r.id,
        date: v.date,
        cancelled: f.elements.cancelled.checked,
      };
      if (!exception.cancelled)
        for (const k of ["move_to", "room", "start", "end"])
          if (v[k]) exception[k] = v[k];
      await nativeCall("assistant_courses", {
        action: "set_exception",
        revision,
        exception,
      });
      notice("本次课程安排已更新");
      await robotCourses(el);
    });
    editor.scrollIntoView({ behavior: "smooth", block: "start" });
  });
  el.querySelector("#course-edit").onclick = () => {
    const editor = el.querySelector("#course-editor");
    let periods = structuredClone(c.periods),
      courses = structuredClone(c.courses);
    editor.innerHTML = robotPanel(
      "编辑学期课表",
      `<form id="course-config"><div class="grid">${robotInput("课表名称", "name", c.name, "text", "required")}${robotInput("第一教学周的周一", "first_week_monday", c.first_week_monday, "date", "required")}${robotInput("教学周数", "total_weeks", c.total_weeks, "number", 'min="1" max="60" required')}</div><h3>每节课的时间</h3><div id="period-rows"></div>${robotButton("添加节次", 'id="period-add"')}<h3 class="section">课程安排</h3><p class="policy">节次和教学周可填写 1,2 或 1-8,10；教学周留空表示每周都有。</p><div id="course-rows"></div>${robotButton("添加一门课程", 'id="course-add"')}<div class="actions section"><button class="primary">保存整张课表</button>${robotButton("取消编辑", 'id="course-config-cancel"')}</div><p class="policy">已有临时调课安排会保留。删除课程前，请先恢复它的临时调整。</p></form>`,
    );
    function periodRow(p, i) {
      return `<div class="robot-edit-row" data-period="${i}">${robotInput("第几节", "number", p.number, "number", 'min="1" required')}${robotInput("开始", "start", p.start, "time", "required")}${robotInput("结束", "end", p.end, "time", "required")}${robotButton("移除", `data-period-remove="${i}"`)}</div>`;
    }
    function courseRow(r, i) {
      return `<div class="robot-course-editor" data-course="${i}"><div class="grid">${robotInput("课程名称", "title", r.title, "text", "required")}${robotSelect("星期", "weekday", { 1: "周一", 2: "周二", 3: "周三", 4: "周四", 5: "周五", 6: "周六", 7: "周日" }, r.weekday)}${robotInput("节次", "periods", r.periods.join(","), "text", "required")}${robotInput("教学周（留空每周）", "weeks", r.weeks.join(","), "text")}${robotInput("教室", "room", r.room, "text")}${robotInput("教师（可选）", "teacher", r.teacher || "", "text")}</div>${robotButton("移除这门课", `data-course-remove="${i}"`)}</div>`;
    }
    function capture() {
      periods = [...editor.querySelectorAll("[data-period]")].map((row) => ({
        number: +row.querySelector("[name=number]").value,
        start: row.querySelector("[name=start]").value,
        end: row.querySelector("[name=end]").value,
      }));
      courses = [...editor.querySelectorAll("[data-course]")].map((row) => {
        const original = courses[+row.dataset.course],
          v = (n) => row.querySelector(`[name=${n}]`).value;
        return {
          ...original,
          title: v("title"),
          weekday: +v("weekday"),
          periods: robotNumberList(v("periods")),
          weeks: robotNumberList(v("weeks"), 60),
          room: v("room"),
          teacher: v("teacher"),
        };
      });
    }
    function paint() {
      editor.querySelector("#period-rows").innerHTML = periods
        .map(periodRow)
        .join("");
      editor.querySelector("#course-rows").innerHTML = courses
        .map(courseRow)
        .join("");
      robotAction(editor, "[data-period-remove]", (b) => {
        capture();
        periods.splice(+b.dataset.periodRemove, 1);
        paint();
      });
      robotAction(editor, "[data-course-remove]", (b) => {
        capture();
        const index = +b.dataset.courseRemove;
        if (c.exceptions?.some((x) => x.course_id === courses[index].id))
          throw Error("请先恢复这门课的临时调整");
        courses.splice(index, 1);
        paint();
      });
    }
    robotAction(editor, "#period-add", () => {
      capture();
      periods.push({
        number: Math.max(0, ...periods.map((p) => p.number)) + 1,
        start: "08:00",
        end: "08:45",
      });
      paint();
    });
    robotAction(editor, "#course-add", () => {
      capture();
      courses.push({
        id: "web-" + crypto.randomUUID().slice(0, 8),
        title: "",
        weekday: 1,
        periods: [],
        weeks: [],
        room: "",
        teacher: "",
      });
      paint();
    });
    editor.querySelector("#course-config-cancel").onclick = () =>
      (editor.innerHTML = "");
    bindForm("course-config", async (b) => {
      capture();
      const config = {
        ...c,
        name: b.name,
        first_week_monday: b.first_week_monday,
        total_weeks: Number(b.total_weeks),
        timezone: "Asia/Shanghai",
        periods,
        courses,
      };
      await nativeCall("assistant_courses", {
        action: "import",
        revision,
        config,
      });
      notice("课表已保存");
      await robotCourses(el);
    });
    paint();
    editor.scrollIntoView({ behavior: "smooth", block: "start" });
  };
}
async function robotWeather(el) {
  const result = await robotRead("assistant_weather", { action: "status" }),
    location = await robotRead("assistant_location", { action: "status" }),
    s = result.value,
    places = s?.places || {};
  robotPage(
    el,
    "assistant_weather",
    robotPanel(
      "关注地点的天气",
      s
        ? `<form id="weather-query"><div class="grid">${robotSelect("查询地点", "place", Object.fromEntries([...(!places[s.candidate?.place_id] ? [["", "请选择要查询的地点"]] : []), ...Object.entries(places).map(([id, p]) => [id, [p.name, p.region].filter(Boolean).join(" · ")])]), s.candidate?.place_id)}${robotInput("日期", "date", robotDay(), "date", "required")}</div><button class="primary">查询天气</button><p class="policy">按你选择的地点查询，不代表实时定位。查询可能使用天气服务额度。</p></form><div id="weather-result">${robotEmpty("选择地点与日期，查看天气和出行相关信息。")}</div>`
        : `<p>天气暂时不可用。${esc(result.error.message)}</p><p>连接你自己的和风天气服务后，即可查询天气。</p>`,
    ) +
      (location.value ? robotLocationSection(location.value) : "") +
      robotPanel(
        "天气服务与播报",
        `<div class="actions">${ws?.instance_managed ? robotButton("天气服务接入", 'id="weather-connect"') : ""}${robotButton("安排天气播报", 'data-robot-module="assistant_heartbeat"')}${robotAsk("我想调整天气关注地点，请先查看我现有的地点与行程，说明可以怎样调整。", "讨论关注地点")}</div>${s?.candidate ? `<p class="policy">今天的日程默认地点：${esc(s.candidate.name)}。这是默认安排，不是实际位置。</p>` : ""}`,
      ),
  );
  robotBindLocation(el, location.value, () => robotWeather(el));
  el.querySelector("#weather-connect")?.addEventListener("click", () =>
    nativeWeatherSetup(el),
  );
  if (s)
    bindForm("weather-query", async (b) => {
      if (!b.place)
        throw Error("请先选择要查询的地点；临时目的地可在对话中请助手查询");
      const out = el.querySelector("#weather-result");
      out.innerHTML = '<p class="loading">正在查询天气…</p>';
      try {
        const d = await nativeCall("assistant_weather", {
          action: "query",
          place: b.place,
          date: b.date,
          view: "overview",
          components: ["current", "daily", "hourly", "alerts"],
          location_source: "explicit_question",
          location_reason: "用户在天气页面选择此地点和日期",
        });
        const part = (key, render) => {
          const p = d.components?.[key];
          if (!p || !["fresh", "cached", "stale"].includes(p.status))
            return `<p class="muted">${{ current: "当前天气", daily: "每日预报", hourly: "逐小时预报", alerts: "天气预警" }[key]}暂时不可用</p>`;
          return (
            (p.status === "stale"
              ? '<p class="warning">以下为旧数据，更新未成功，仅供参考。</p>'
              : "") +
            render(p.data || {}) +
            `<p class="policy">数据获取：${esc(robotTime(p.fetched_at))}</p>`
          );
        };
        const quantity = (v) =>
          v && typeof v === "object"
            ? v.value === undefined
              ? "暂无"
              : esc(String(v.value)) + (v.unit ? esc(v.unit) : "")
            : v === undefined
              ? "暂无"
              : esc(v);
        out.innerHTML = `<div class="section"><h3>${esc(d.place?.name || places[b.place]?.name || "所选地点")} · ${esc(b.date)}</h3>${part("current", (v) => `<p class="robot-summary">${esc(v.weather?.text || v.text || "当前天气")} · ${quantity(v.temperature)}</p>`)}${part(
          "daily",
          (v) =>
            robotRows(v.days)
              .map((day) =>
                robotArticle(
                  String(day.forecastStartTime || day.fxDate || b.date).slice(
                    0,
                    10,
                  ),
                  `<p>${esc(day.daytime?.weather?.text || day.textDay || "天气预报")} · 最低 ${quantity(day.temperatureMin)} / 最高 ${quantity(day.temperatureMax)}</p>`,
                ),
              )
              .join("") || "<p>暂无每日预报</p>",
        )}${part(
          "hourly",
          (v) =>
            `<div class="robot-hourly">${
              robotRows(v.hours)
                .slice(0, 12)
                .map(
                  (h) =>
                    `<div><strong>${esc(robotTime(h.forecastTime || h.fxTime))}</strong><span>${esc(h.weather?.text || h.text || "")} ${quantity(h.temperature)}</span></div>`,
                )
                .join("") || "<p>暂无逐小时预报</p>"
            }</div>`,
        )}${part(
          "alerts",
          (v) =>
            robotRows(v.alerts || v.warning)
              .map((a) =>
                robotArticle(
                  a.headline || a.title || "天气预警",
                  `<p>${esc(a.description || a.text || "")}</p>`,
                ),
              )
              .join("") ||
            "<p>本次天气来源未返回预警；上海官方预警可在天气预警模块另行查看。</p>",
        )}<p class="policy">当前天气反映现在，未来日期请参考每日与逐小时预报。来源：${robotLink(d.attribution || "https://www.qweather.com/", "和风天气")}</p></div>`;
      } catch (e) {
        out.innerHTML = robotError(e);
      }
    });
}
function robotLocationSection(d) {
  const estimate = d?.current,
    c = d?.config,
    known = estimate?.known,
    places = c?.places || {},
    weekday = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
  return robotPanel(
    "地点与行程",
    `<div class="robot-location-current"><h3>${known ? esc(estimate.place.name) : "当前地点待确认"}</h3><p>${known ? esc([estimate.place.city, estimate.place.district].filter(Boolean).join(" · ")) : ""}</p><p class="muted">${esc(estimate?.basis || "添加常用地点与明确行程，帮助助手判断预警是否与你有关。")}</p></div>${c?.weekly?.length ? `<div class="robot-location-week">${c.weekly.map((w) => `<p><strong>${weekday[w.weekday]} ${esc(w.clock)} 起</strong><span>按${esc(places[w.place_id]?.name || "已设地点")}推断</span></p>`).join("")}</div>` : ""}<p class="policy">明确的临时行程优先于每周安排；结束后恢复默认。所有时间为北京时间，这不是实时定位。上海全市预警在上海期间仍适用。</p><details><summary>添加或管理临时行程</summary>${Object.keys(places).length ? `<form id="location-plan-form" class="section"><div class="grid">${robotSelect("这段时间在哪里", "place_id", Object.fromEntries(Object.entries(places).map(([id, p]) => [id, p.name])), Object.keys(places)[0])}${robotInput("行程说明", "label", "", "text", 'required maxlength="120" placeholder="例如：这周末提前返校"')}${robotInput("开始时间", "starts_at", "", "datetime-local", "required")}${robotInput("结束时间", "ends_at", "", "datetime-local", "required")}</div><button class="primary">保存临时行程</button></form>` : '<p class="policy">先添加一个常用地点，再安排临时行程。</p>'}${robotRows(
      c?.plans,
    )
      .map((p) =>
        robotArticle(
          p.label || places[p.place_id]?.name || "临时行程",
          `<p>${esc(places[p.place_id]?.name || "")} · ${esc(robotTime(p.starts_at))} 至 ${esc(robotTime(p.ends_at))}</p>`,
          robotButton("移除行程", `data-location-remove="${esc(p.id)}"`),
        ),
      )
      .join(
        "",
      )}</details><details><summary>管理常用地点</summary><p class="policy">这里只记录城市与地区，用于筛选预警。天气查询的经纬度沿用天气服务设置；新目的地可请助手查询。</p>${Object.entries(
      places,
    )
      .map(
        ([, p]) =>
          `<p>${esc(p.name)} · ${esc(p.city)} ${esc(p.district || "")}</p>`,
      )
      .join(
        "",
      )}<form id="location-place-form" class="section"><div class="grid">${robotInput("地点名称", "name", "", "text", 'required maxlength="60" placeholder="例如：出差地点"')}${robotInput("城市", "city", "上海", "text", 'required maxlength="40"')}${robotSelect("在上海的地区", "district", Object.fromEntries(["黄浦区", "徐汇区", "长宁区", "静安区", "普陀区", "虹口区", "杨浦区", "浦东新区", "闵行区", "宝山区", "嘉定区", "金山区", "松江区", "青浦区", "奉贤区", "崇明区"].map((x) => [x, x])), "浦东新区")}</div><button>添加常用地点</button></form></details>`,
  );
}
function robotBindLocation(el, d, refresh) {
  const c = d?.config;
  if (!c) return;
  const mutate = async (args) => {
    await nativeCall("assistant_location", { revision: c.revision, ...args });
    notice("地点安排已更新");
    await refresh();
  };
  bindForm("location-plan-form", (b) => {
    if (b.starts_at >= b.ends_at) throw Error("结束时间应晚于开始时间");
    return mutate({
      action: "set_plan",
      place_id: b.place_id,
      label: b.label,
      starts_at: robotISO(b.starts_at),
      ends_at: robotISO(b.ends_at),
    });
  });
  bindForm("location-place-form", (b) =>
    mutate({
      action: "set_place",
      place_id: "place-" + crypto.randomUUID().slice(0, 8),
      place: {
        name: b.name,
        city: ["上海", "上海市"].includes(b.city.trim())
          ? "上海"
          : b.city.trim(),
        district: ["上海", "上海市"].includes(b.city.trim()) ? b.district : "",
      },
    }),
  );
  robotAction(el, "[data-location-remove]", async (b) => {
    if (confirm("移除这条临时行程？该时段将重新按其他行程或默认规则推断。"))
      await mutate({ action: "remove_plan", id: b.dataset.locationRemove });
  });
  const city = el.querySelector("#location-place-form [name=city]"),
    district = el.querySelector("#location-place-form [name=district]");
  if (city && district)
    city.oninput = () =>
      (district.closest("label").hidden = !["上海", "上海市"].includes(
        city.value.trim(),
      ));
}
function robotAlertRecord(a, meta) {
  const body = robotText(a.body);
  return robotArticle(
    `${a.area} ${a.hazard}${a.level}预警`,
    `<p class="muted">${esc(meta)} · 发布于 ${esc(robotTime(a.published_at))}</p>${body ? `<p class="robot-alert-summary">${esc(body.length > 100 ? body.slice(0, 100) + "…" : body)}</p>` : ""}<details><summary>完整预警与官方来源</summary><div class="robot-prose">${esc(body || "暂无完整正文")}</div><p>${robotLink(a.url, "查看官方原文")}</p></details>`,
  );
}
async function robotAlerts(el) {
  const d = await nativeCall("assistant_weather_alerts", { action: "status" }),
    c = d.config,
    records = robotRows(d.records),
    current = records.filter(
      (r) =>
        r.applicable !== false &&
        ["observed_active", "not_reconfirmed"].includes(r.state),
    ),
    history = d.notification_history;
  const sourceText =
    {
      healthy: "官方来源最近检查正常",
      degraded: "部分官方来源暂时不可用，信息可能不完整",
      unavailable: "暂时无法取得最新预警，请同时关注官方渠道",
      not_checked: "尚未检查官方来源",
    }[d.coverage] || "来源状态待核实";
  robotPage(
    el,
    "assistant_weather_alerts",
    robotPanel(
      "当前与你相关的预警",
      `<p class="${d.coverage === "healthy" ? "muted" : "warning"}">${esc(sourceText)}</p>${current.map((r) => robotAlertRecord(r.record.alert, r.state === "not_reconfirmed" ? "尚未重新确认有效性，请核对官方信息" : "最近采集到的有效预警")).join("") || robotEmpty(d.location?.current?.known && d.location.current.place.city !== "上海" ? "按当前行程推测你不在上海，暂停上海预警通知。" : d.coverage === "healthy" ? "当前没有与你推测所在地匹配的有效预警。" : "目前没有可确认的相关预警，来源异常不代表没有预警。")}<p class="policy">按与实际通知相同的地点规则筛选，外区预警不在这里展示。过去发过的通知见下方记录。</p>`,
    ) +
      (d.location ? robotLocationSection(d.location) : "") +
      robotPanel(
        "预警发送记录",
        `<p class="muted">${history ? "共记录 " + history.total + " 次发送。" : "读取历史记录后在这里显示。"}历史按发送当时的范围保留，不按你现在的地点隐藏；发送记录不等于设备收件确认。</p><div id="alert-history"></div><div id="alert-history-more"></div>`,
      ) +
      robotPanel(
        "通知设置",
        `<form id="alert-settings">${robotCheck("自动检查并通知相关天气预警", "enabled", c.enabled)}${robotInput("检查间隔（分钟）", "minutes", c.poll_seconds / 60, "number", 'min="5" max="60" required')}<button class="primary">保存预警设置</button></form>${robotScheduleLink()}`,
      ) +
      robotPanel(
        "已结束的相关预警",
        records
          .filter(
            (r) =>
              r.applicable !== false &&
              !["observed_active", "not_reconfirmed"].includes(r.state),
          )
          .map((r) =>
            robotAlertRecord(
              r.record.alert,
              r.state === "explicitly_cancelled"
                ? "官方明确解除"
                : "已过有效期，不代表官方明确解除",
            ),
          )
          .join("") || robotEmpty("暂无已结束的相关预警。"),
      ),
  );
  function paintHistory(page, append = false) {
    const out = el.querySelector("#alert-history"),
      more = el.querySelector("#alert-history-more");
    if (!out) return;
    const html = robotRows(page.events)
      .map((e) =>
        robotAlertRecord(
          e.alert,
          `发送于 ${robotTime(e.delivered_at)} · ${{ new: "新发布", upgrade: "等级升级", update: "预警更新", cancel: "官方解除" }[e.kind] || "预警通知"}`,
        ),
      )
      .join("");
    if (append) out.insertAdjacentHTML("beforeend", html);
    else out.innerHTML = html || robotEmpty("目前没有已发送的预警记录。");
    more.innerHTML = page.has_more
      ? robotButton("加载更早的发送记录", 'id="alert-load-history"')
      : "";
    robotAction(more, "#alert-load-history", async () =>
      paintHistory(
        await nativeCall("assistant_weather_alerts", {
          action: "history",
          offset: page.next_offset,
          limit: 20,
        }),
        true,
      ),
    );
  }
  if (history) paintHistory(history);
  else
    el.querySelector("#alert-history").innerHTML =
      robotEmpty("当前服务暂未提供完整发送记录。");
  bindForm("alert-settings", async (b, f) => {
    await nativeCall("assistant_weather_alerts", {
      action: "configure",
      revision: c.revision,
      enabled: f.elements.enabled.checked,
      poll_seconds: Number(b.minutes) * 60,
    });
    notice("预警设置已更新");
    await robotAlerts(el);
  });
  robotBindLocation(el, d.location, () => robotAlerts(el));
}

async function robotGitHub(el) {
  const d = await nativeCall("assistant_github", { action: "status" }),
    c = d.config,
    rows = robotRows(d.watches);
  robotPage(
    el,
    "assistant_github",
    robotPanel(
      "正在跟踪的 PR",
      `<form id="github-add" class="actions">${robotInput("公开 PR 链接", "url", "", "url", 'required placeholder="https://github.com/owner/repo/pull/123"')}<button class="primary">添加跟踪</button></form>${
        rows
          .map((r, i) => {
            const s = r.baseline;
            return robotArticle(
              s?.title || r.repository + " #" + r.number,
              `<p>${robotLink(r.url, r.repository + " #" + r.number)} · ${r.excluded ? "已排除" : r.finished ? "跟踪已结束" : r.enabled ? "跟踪中" : "已暂停"}</p>${s ? `<p>${esc(robotStatus(s.state))}${s.ci?.failed?.length ? " · 检查失败：" + esc(s.ci.failed.join("、")) : ""}</p>` : ""}<p class="muted">最近检查：${esc(robotTime(r.checked_at))}${r.error ? " · 检查未成功，请稍后重试" : ""}</p>`,
              robotButton("读取最新进展", `data-github-query="${i}"`) +
                (!r.excluded
                  ? robotButton("停止跟踪", `data-github-remove="${i}"`)
                  : ""),
            );
          })
          .join("") ||
        robotEmpty("粘贴一条公开 PR 链接，跟进讨论、审核和检查变化。")
      }<div id="github-detail"></div>`,
    ) +
      robotPanel(
        "跟踪与通知设置",
        `<form id="github-settings">${robotInput("凭据所属的 GitHub 用户名", "login", c.login || "", "text", "required")}${robotCheck("启用 PR 跟踪", "enabled", c.enabled)}${robotCheck("自动发现我的公开 PR", "auto_discover", c.auto_discover)}<div class="grid">${robotInput("进展检查间隔（分钟）", "poll", c.poll_seconds / 60 || 1, "number", 'min="1" max="60" required')}${robotInput("发现新 PR 的间隔（分钟）", "discovery", c.discovery_seconds / 60 || 5, "number", 'min="5" max="60" required')}</div><button class="primary">保存跟踪设置</button></form><div class="actions section">${ws?.instance_managed ? robotButton("管理 GitHub 令牌", 'id="github-connect"') : ""}${robotButton("发现我的公开 PR", 'id="github-discover"')}</div><p class="policy">目前支持公开 PR 的只读跟踪，不会发表评论、合并 PR 或重跑检查。</p>${robotScheduleLink()}`,
      ),
  );
  bindForm("github-add", async (b) => {
    await nativeCall("assistant_github", { action: "add", url: b.url });
    notice("已添加跟踪，首次读取作为基准，不重发历史变化");
    await robotGitHub(el);
  });
  bindForm("github-settings", async (b, f) => {
    await nativeCall("assistant_github", {
      action: "configure",
      revision: c.revision,
      login: b.login,
      enabled: f.elements.enabled.checked,
      auto_discover: f.elements.auto_discover.checked,
      poll_seconds: Number(b.poll) * 60,
      discovery_seconds: Number(b.discovery) * 60,
    });
    notice("跟踪设置已更新");
    await robotGitHub(el);
  });
  el.querySelector("#github-connect")?.addEventListener("click", () =>
    nativeGitHubSetup(el),
  );
  robotAction(el, "#github-discover", async () => {
    await nativeCall("assistant_github", { action: "discover" });
    notice("已更新公开 PR 列表");
    await robotGitHub(el);
  });
  robotAction(el, "[data-github-remove]", async (b) => {
    const r = rows[+b.dataset.githubRemove];
    if (!confirm("停止跟踪这条 PR，并从自动发现中排除？")) return;
    await nativeCall("assistant_github", {
      action: "remove",
      id: r.id,
      revision: r.revision,
    });
    await robotGitHub(el);
  });
  robotAction(el, "[data-github-query]", async (b) => {
    const r = rows[+b.dataset.githubQuery],
      s = await nativeCall("assistant_github", { action: "query", id: r.id }),
      out = el.querySelector("#github-detail");
    out.innerHTML = `<div class="section"><h3>${esc(s.title)}</h3><p>${esc(robotStatus(s.state))} · ${esc(robotTime(s.fetched_at))}</p>${
      Object.values(s.items || {})
        .slice(-20)
        .map((item) =>
          robotArticle(
            item.author || item.kind || "PR 动态",
            `<p>${esc(item.body || item.text || "")}</p>${item.url ? robotLink(item.url, "查看原始动态") : ""}`,
          ),
        )
        .join("") || "<p>本次没有可显示的讨论内容。</p>"
    }</div>`;
    out.scrollIntoView({ behavior: "smooth", block: "start" });
  });
}
async function robotNews(el) {
  const latest = await robotRead("assistant_ai_news", { action: "latest" }),
    d = latest.value;
  const message = robotText(d?.message),
    events = robotRows(d?.output?.events || d?.events || d?.report?.events);
  robotPage(
    el,
    "assistant_ai_news",
    robotPanel(
      "最近一期简报",
      message
        ? `<div class="robot-prose">${esc(message)}</div>`
        : events.length
          ? events
              .map((e) => robotArticle(e.title, `<p>${esc(e.summary)}</p>`))
              .join("")
          : robotEmpty(
              "暂时没有可读取的已播报简报。可以阅读当前新闻候选，或安排自动播报。",
            ),
    ) +
      robotPanel(
        "阅读新闻",
        `${robotButton("获取近期新闻", 'id="news-load"', true)}<p class="policy">读取新闻不会另行发送微信通知。下方候选标题来自外部来源，尚未由助手整理成中文简报。</p><div id="news-list"></div><div id="news-reader"></div>`,
      ) +
      robotPanel(
        "新闻播报安排",
        `<p>在自动安排中设置 AI 新闻的接收时间，也可以直接请助手整理一份简报。</p><div class="actions">${robotButton("调整播报时间", 'data-robot-module="assistant_heartbeat"')}${robotAsk("请为我整理近期 AI 新闻，阅读来源后用简短中文说明重点。", "让助手整理新闻")}</div>`,
      ),
  );
  robotAction(el, "#news-load", async () => {
    const result = await nativeCall("assistant_ai_news", { action: "list" }),
      out = el.querySelector("#news-list");
    out.innerHTML =
      robotRows(result.candidates)
        .slice(0, 20)
        .map((a, i) =>
          robotArticle(
            a.title,
            `<p class="muted">${esc(a.sources?.join("、") || "新闻来源")} · ${esc(robotTime(a.published_at))}</p>`,
            robotButton("阅读来源", `data-news-read="${i}"`),
          ),
        )
        .join("") || robotEmpty("本次没有取得可读的新闻。请稍后重试。");
    robotAction(out, "[data-news-read]", async (b) => {
      const a = result.candidates[+b.dataset.newsRead],
        data = await nativeCall("assistant_ai_news", {
          action: "read",
          snapshot_id: result.id,
          ids: [a.id],
        }),
        reader = el.querySelector("#news-reader");
      reader.innerHTML = robotRows(data.articles)
        .map(
          (item) =>
            `<section class="section"><h3>${esc(item.title)}</h3><p>${robotLink(item.url || item.evidence_url, "打开原始来源")}</p><div class="robot-prose">${esc(item.text || "当前没有取得可读的正文片段，请打开原始来源。")}</div><p class="policy">这里展示来源提供的有界正文片段，可能并非全文。</p></section>`,
        )
        .join("");
      reader.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  });
}
async function robotBriefing(el, tool) {
  const morning = tool === "assistant_morning",
    result = await robotRead(tool, { action: "latest" }),
    d = result.value,
    message = robotText(d?.message),
    snapshot = d?.snapshot;
  robotPage(
    el,
    tool,
    robotPanel(
      morning ? "最近一次晨报" : "最近一次晚间总结",
      message
        ? `${snapshot?.date ? `<p class="muted">${esc(snapshot.date)}</p>` : ""}<div class="robot-prose robot-report">${esc(message)}</div>`
        : result.error && !/no rows|not found/i.test(result.error.message)
          ? robotError(result.error)
          : robotEmpty(
              morning
                ? "暂时没有已生成的晨报。启用自动安排后，可以在这里回看天气、课程和重要事项。"
                : "暂时没有已生成的晚间总结。启用自动安排后，可以在这里回看未完成事项与明日安排。",
            ),
    ) +
      robotPanel(
        "接收安排",
        `<p>${morning ? "晨报会结合实际可用的天气、课程、待办、邮件和新闻信息。" : "晚间总结会结合未完成事项、明日课程和提醒信息。"}内容取决于你已接入的服务和实际数据。</p><div id="briefing-schedule"><p class="loading">正在读取安排…</p></div><div class="actions">${robotButton("调整作息和自动安排", 'data-robot-module="assistant_heartbeat"')}${robotAsk(morning ? "请查看我的晨报安排，帮我调整接收时间。" : "请查看我的晚间总结安排，帮我调整接收时间。", "与助手讨论安排")}</div>`,
      ) +
      (morning
        ? robotPanel(
            "让晨报更贴近你的日常",
            `<div class="actions">${robotButton("查看课程表", 'data-robot-module="assistant_courses"')}${robotButton("管理邮箱", 'data-robot-module="assistant_mail"')}${robotButton("查看天气", 'data-robot-module="assistant_weather"')}</div>`,
          )
        : robotPanel(
            "准备明天",
            `<div class="actions">${robotButton("整理未完成事项", 'data-robot-module="assistant_tasks"')}${robotButton("查看课程安排", 'data-robot-module="assistant_courses"')}</div>`,
          )),
  );
  const schedule = await robotRead("assistant_heartbeat", { action: "get" }),
    target = morning ? "morning_briefing" : "evening_briefing",
    out = el.querySelector("#briefing-schedule");
  if (!out) return;
  if (schedule.error) {
    out.innerHTML = robotError(schedule.error);
    return;
  }
  const c = schedule.value.config,
    triggers = Object.values(c.triggers || {}).filter(
      (t) =>
        t.modules?.includes(target) ||
        Object.values(t.weekday_modules || {}).some((v) => v.includes(target)),
    );
  out.innerHTML =
    triggers
      .map(
        (t) =>
          `<p>${esc(t.title)}：${esc(robotTriggerText(t))} · ${t.enabled && c.module_enabled?.[target] ? "已启用" : "已暂停"}</p>`,
      )
      .join("") || "<p>还没有安排自动接收。可在“作息与自动安排”中添加。</p>";
}
async function robotProposals(el) {
  const rows = robotRows(
    await nativeCall("assistant_proposals", { action: "list" }),
  );
  robotPage(
    el,
    "assistant_proposals",
    robotPanel(
      "由你确认的事项",
      rows
        .map((p) => {
          const c = p.candidate;
          return robotArticle(
            c.title,
            `<p>${esc(p.question || "是否把这件事记成待办？")}</p>${c.due_at || c.due_date ? `<p class="muted">识别到的期限：${esc(c.due_at ? robotTime(c.due_at) : c.due_date)}</p>` : ""}<details><summary>为什么提出这条建议</summary><blockquote>${esc(c.evidence_quote || "当前没有可显示的来源片段")}</blockquote><p class="policy">来自已分析邮件的内容，仅供你核对。建议尚未创建待办。</p></details>`,
            robotAsk(
              `关于待办建议「${c.title}」，请先核对来源和期限，向我说明将创建什么，并等待我确认。`,
              "与助手确认",
            ) +
              robotAsk(
                `我不要记录「${c.title}」这条待办建议，请核对后拒绝这条建议。`,
                "准备忽略",
              ),
          );
        })
        .join("") ||
        robotEmpty(
          "暂时没有需要确认的建议。助手发现邮件中的具体事项时，会在这里列出。",
        ),
    ) +
      robotPanel(
        "确认后才执行",
        "<p>建议不会自动变成待办。点击操作会把相关内容带到对话输入框，由你发送；加入待办前，助手仍会核对必要的时间并等待明确确认。</p>",
      ),
  );
}
async function robotActivity(el) {
  const [events, runs] = await Promise.all([
    api("/activity"),
    ws?.native_mode
      ? robotRead("assistant_heartbeat", { action: "runs" })
      : Promise.resolve({ value: null }),
  ]);
  const moduleNames = {
    schoolday_reminder: "教学日 UU 电脑提醒",
    mail_check: "邮箱检查",
    morning_briefing: "晨报",
    evening_briefing: "晚间总结",
    course_reminder: "课前提醒",
    subscription_reminder: "到期提醒",
    ai_news_briefing: "AI 新闻",
    weather_briefing: "天气播报",
    github_pr_check: "GitHub 跟踪",
    weather_alert_check: "天气预警",
  };
  const entries = robotRows(
    runs.value?.runs || runs.value?.items || runs.value,
  );
  if (!el.isConnected) return;
  el.innerHTML =
    head("助手动态", "了解自动服务的执行情况，找到需要你处理的问题。") +
    robotPanel(
      "自动服务动态",
      runs.error
        ? robotError(runs.error)
        : entries.length
          ? entries
              .slice(0, 40)
              .map((r) =>
                robotArticle(
                  r.title || moduleNames[r.module_id || r.module] || "自动服务",
                  `<p>${esc(robotStatus(r.status || r.last_run?.status))} · ${esc(robotTime(r.startedAt || r.started_at || r.created_at || r.last_run?.started_at))}</p>${r.decisionReason || r.reason || r.error ? `<p>${esc(r.decisionReason || r.reason || r.error)}</p>` : ""}`,
                ),
              )
              .join("")
          : robotEmpty("暂无可显示的自动服务执行记录。"),
    ) +
    robotPanel(
      "账号与操作记录",
      robotRows(events)
        .map((r) =>
          robotArticle(
            r.event,
            `<p class="muted">${esc(robotTime(r.created))} · ${esc(robotStatus(r.status))}</p>`,
          ),
        )
        .join("") || robotEmpty("暂时没有操作记录。"),
    );
}
const robotPreferenceStart = "[AS Robot 网页回答偏好]";
const robotPreferenceEnd = "[/AS Robot 网页回答偏好]";
function robotPreferences(prompt) {
  const start = prompt.indexOf(robotPreferenceStart),
    end = prompt.indexOf(robotPreferenceEnd, start);
  return start >= 0 && end >= start
    ? prompt.slice(start + robotPreferenceStart.length, end).trim()
    : "";
}
function robotMergePreferences(prompt, text) {
  const start = prompt.indexOf(robotPreferenceStart),
    end = prompt.indexOf(robotPreferenceEnd, start),
    base =
      start >= 0 && end >= start
        ? prompt.slice(0, start) + prompt.slice(end + robotPreferenceEnd.length)
        : prompt;
  return (
    base.trimEnd() +
    (text.trim()
      ? `\n\n${robotPreferenceStart}\n${text.trim()}\n${robotPreferenceEnd}`
      : "")
  );
}
async function robotAssistant(el) {
  const [p, identity] = await Promise.all([
    api("/native/profile"),
    ws?.instance_managed ? api("/native/identity") : Promise.resolve(null),
  ]);
  if (!el.isConnected) return;
  el.innerHTML =
    head(
      "助手设置",
      "决定助手怎样称呼你、怎样表达，以及你希望它了解的回答偏好。",
    ) +
    (identity
      ? robotPanel(
          "名称与称呼",
          `<form id="robot-identity"><div class="grid">${robotInput("助手的名字", "name", identity.name, "text", 'required maxlength="32"')}${robotInput("如何称呼你", "address", identity.address || "", "text", 'maxlength="32" placeholder="例如：小林"')}</div><button class="primary">保存名称与称呼</button></form>`,
        )
      : "") +
    robotPanel(
      "你喜欢的回答方式",
      `<form id="robot-preferences"><label>回答偏好<textarea name="preferences" maxlength="4000" placeholder="例如：先给结论；回答简短；涉及操作时给具体步骤；不主动使用表情。">${esc(robotPreferences(p.prompt))}</textarea></label><p class="policy">补充这里的偏好会保留原有完整提示词。偏好影响表达，不会扩大助手的操作权限。</p><button class="primary">保存回答偏好</button></form>`,
    ) +
    robotPanel(
      "日常安排",
      `<div class="robot-quicklinks"><button data-robot-module="assistant_memory"><strong>个人记忆</strong><span>纠正偏好与临时安排</span></button><button data-robot-module="assistant_heartbeat"><strong>作息与自动安排</strong><span>主动通知的时间与频率</span></button><button data-go="native"><strong>选择功能</strong><span>按需要接入和管理服务</span></button></div>`,
    ) +
    robotPanel(
      "高级：完整提示词",
      `<details><summary>查看并编辑完整提示词</summary><form id="robot-full-prompt" class="section"><p class="policy">这里修改助手的完整行为说明。保存会替换整份内容，请保留仍需使用的规则。</p><label>完整提示词<textarea class="native-prompt" name="prompt" required>${esc(p.prompt)}</textarea></label><button>保存完整提示词</button></form></details>`,
    ) +
    robotPanel(
      "让微信助手使用新设置",
      `<p>名称与称呼保存后生效。修改回答偏好或完整提示词后，网页下一轮对话会读取新设置；微信助手需要重新加载。</p>${robotButton("重新加载助手设置", 'id="robot-apply"')}<p class="policy">重新加载会短暂重连微信与后台进程，已有事项和数据保留。</p>`,
    );
  if (identity)
    bindForm("robot-identity", async (b) => {
      await api("/native/identity", {
        method: "POST",
        body: { revision: identity.revision, name: b.name, address: b.address },
      });
      notice("名称与称呼已保存");
      await robotAssistant(el);
    });
  bindForm("robot-preferences", async (b) => {
    if (
      b.preferences.includes(robotPreferenceStart) ||
      b.preferences.includes(robotPreferenceEnd)
    )
      throw Error("请直接填写回答偏好，不要包含系统分隔标记");
    await api("/native/profile", {
      method: "POST",
      body: {
        revision: p.revision,
        prompt: robotMergePreferences(p.prompt, b.preferences),
      },
    });
    notice("回答偏好已保存；微信助手需重新加载设置");
    await robotAssistant(el);
  });
  bindForm("robot-full-prompt", async (b) => {
    await api("/native/profile", {
      method: "POST",
      body: { revision: p.revision, prompt: b.prompt },
    });
    notice("完整提示词已保存；微信助手需重新加载设置");
    await robotAssistant(el);
  });
  robotAction(el, "#robot-apply", async () => {
    if (!confirm("重新加载设置将短暂重连微信和后台进程。继续？")) return;
    await api("/native/apply", { method: "POST", body: {} });
    notice("助手已重新加载设置");
  });
  robotBind(el);
}
