// Synthetic owner data for DOM and browser QA; never route tests to a live account.
const week = Object.fromEntries(
  [
    "monday",
    "tuesday",
    "wednesday",
    "thursday",
    "friday",
    "saturday",
    "sunday",
  ].map((k) => [k, { enabled: true, start: "08:00", end: "24:00" }]),
);
const modules = [
  ["morning_briefing", "晨报"],
  ["mail_check", "邮箱检查"],
  ["schoolday_reminder", "教学日 UU 电脑提醒"],
  ["evening_briefing", "晚间总结"],
  ["course_reminder", "课前提醒"],
  ["subscription_reminder", "到期提醒"],
  ["ai_news_briefing", "AI 新闻"],
].map(([id, name]) => ({ id, name }));
const heartbeat = {
  config: {
    revision: 7,
    timezone: "Asia/Shanghai",
    weekly_schedule: week,
    date_overrides: {
      "2099-11-08": {
        start: "10:00",
        end: "22:00",
        enabled: true,
        trigger_clocks: { morning: "10:30" },
      },
    },
    module_enabled: Object.fromEntries(modules.map((m) => [m.id, true])),
    triggers: {
      morning: {
        id: "morning",
        title: "早晨的安排",
        mode: "daily",
        enabled: true,
        modules: ["morning_briefing"],
        routine_bound: true,
      },
      mail: {
        id: "mail",
        title: "白天检查邮件",
        mode: "interval",
        enabled: true,
        modules: ["mail_check"],
        interval_seconds: 900,
        routine_bound: true,
      },
      uu: {
        id: "uu",
        title: "教学日 UU 电脑提醒",
        mode: "daily",
        clock: "10:55",
        enabled: true,
        modules: ["schoolday_reminder"],
        routine_bound: true,
      },
      evening: {
        id: "evening",
        title: "晚间总结",
        mode: "daily",
        clock: "23:00",
        enabled: true,
        modules: ["evening_briefing"],
      },
    },
  },
  modules,
  effective: {
    source: "weekly",
    window: { enabled: true, start: "08:00", end: "24:00" },
  },
};
const tasks = [
  {
    id: "task1",
    revision: 4,
    title: "提交课程报告",
    body: "检查附件后提交到课程平台",
    status: "open",
    kind: "todo",
    due_at: "2099-11-06T18:00:00+08:00",
    deadline_reminders: true,
    created_at: "2026-11-04T09:00:00+08:00",
  },
  {
    id: "task2",
    revision: 2,
    title: "周末整理读书笔记",
    body: "整理本周的读书笔记",
    status: "open",
    kind: "reminder",
    created_at: "2026-11-03T09:00:00+08:00",
    schedule: { enabled: true, interval: "168h|weekly:sat@10:00" },
  },
  {
    id: "done1",
    revision: 1,
    title: "已经完成的事项",
    body: "",
    status: "done",
    kind: "todo",
  },
];
const memory = [
  {
    id: "memory1",
    revision: 2,
    subject: "回答偏好",
    body: "先给结论，再说明必要步骤。",
    kind: "long",
    status: "active",
    starts_at: "2026-11-01T00:00:00+08:00",
    updated_at: "2026-11-04T09:00:00+08:00",
  },
  {
    id: "memory2",
    revision: 1,
    subject: "出行安排",
    body: "下周去杭州。",
    kind: "short",
    status: "future",
    starts_at: "2099-11-09T00:00:00+08:00",
    expires_at: "2099-11-12T00:00:00+08:00",
  },
];
const courseConfig = {
  revision: 3,
  name: "秋季学期",
  first_week_monday: "2026-09-07",
  total_weeks: 20,
  timezone: "Asia/Shanghai",
  periods: [
    { number: 1, start: "08:00", end: "08:45" },
    { number: 2, start: "08:55", end: "09:40" },
  ],
  courses: [
    {
      id: "course1",
      title: "编译原理",
      weekday: 3,
      periods: [1, 2],
      weeks: [],
      room: "教学楼 A201",
      teacher: "张老师",
    },
  ],
  exceptions: [],
};
const location = {
  configured: true,
  config: {
    revision: 3,
    places: {
      school: {
        name: "上海电力大学临港校区",
        city: "上海",
        district: "浦东新区",
      },
      home: { name: "家", city: "上海", district: "黄浦区" },
    },
    weekly: [
      { weekday: 0, clock: "17:00", place_id: "school" },
      { weekday: 5, clock: "19:00", place_id: "home" },
    ],
    plans: [],
  },
  current: {
    configured: true,
    known: true,
    place_id: "school",
    place: { name: "上海电力大学临港校区", city: "上海", district: "浦东新区" },
    source: "weekly",
    basis: "根据每周地点安排推测，不代表实时定位",
  },
};
const fixtures = {
  assistant_location: { status: location },
  assistant_tasks: { list: tasks, get: tasks[0] },
  assistant_memory: { list: memory },
  assistant_subscriptions: {
    list: [
      {
        id: "expiry1",
        revision: 2,
        name: "个人域名",
        expires_on: "2099-12-01",
        renewal_mode: "manual",
        reminder_days: 7,
      },
    ],
    get_default: { default_reminder_days: 7 },
  },
  assistant_heartbeat: {
    get: heartbeat,
    runs: {
      runs: [
        {
          module: "morning_briefing",
          status: "completed",
          startedAt: 1793750400000,
          decisionReason: "无需额外通知",
        },
      ],
    },
  },
  assistant_mail: {
    status: {
      sources: [{ id: "qq", address: "demo@example.com" }],
      health: [{ account: "qq", last_success: 1793750400 }],
    },
    list: {
      messages: [
        {
          id: "mail1",
          subject: "课程报告提交安排",
          from: "课程助教",
          received_at: 1793750400,
          snippet: "请于周五前提交报告。",
        },
      ],
      has_more: false,
    },
    search: { messages: [], has_more: false },
    silent: { items: [], has_more: false },
    read: {
      message: { subject: "课程报告提交安排", from: "课程助教" },
      body: "请于周五前提交报告。<script>unsafe</script>",
      has_more: false,
    },
  },
  assistant_academic: {
    status: {
      configured: true,
      calendar: {
        school: "上海电力大学",
        terms: [
          { name: "上学期", start: "2026-09-14", end: "2027-01-22" },
          { name: "下学期", start: "2027-03-01", end: "2027-07-09" },
        ],
        holiday_years: { 2026: true, 2027: false },
        sources: [
          {
            title: "学校官方校历",
            url: "https://xxgk.shiep.edu.cn/502/list.htm",
          },
        ],
      },
      today: {
        date: "2026-11-04",
        known: true,
        teaching: true,
        reason: "学期内正常教学日",
      },
      days: [
        {
          date: "2026-11-04",
          known: true,
          teaching: true,
          reason: "学期内正常教学日",
        },
        {
          date: "2026-11-07",
          known: true,
          teaching: false,
          reason: "学期内周末休息日",
        },
      ],
      sync: { checked_at: "2026-11-04T04:20:00+08:00", errors: [] },
    },
  },
  assistant_courses: {
    status: { configured: true, config: courseConfig },
    query: {
      configured: true,
      in_term: true,
      week: 9,
      courses: [
        {
          title: "编译原理",
          starts_at: "2026-11-04T08:00:00+08:00",
          ends_at: "2026-11-04T09:40:00+08:00",
          room: "教学楼 A201",
        },
      ],
    },
  },
  assistant_weather: {
    status: {
      candidate: { place_id: "home", name: "家附近" },
      places: { home: { name: "家附近", region: "上海" } },
      default_by_weekday: {},
    },
    query: {
      place: { name: "家附近" },
      date: "2026-11-04",
      queried_at: "2026-11-04T10:00:00+08:00",
      components: {
        current: {
          status: "fresh",
          fetched_at: "2026-11-04T10:00:00+08:00",
          data: {
            weather: { text: "多云" },
            temperature: { value: 20, unit: "C" },
          },
        },
        daily: {
          status: "fresh",
          data: {
            days: [
              {
                forecastStartTime: "2026-11-04T00:00:00+08:00",
                temperatureMin: { value: 15, unit: "C" },
                temperatureMax: { value: 22, unit: "C" },
                daytime: { weather: { text: "多云" } },
              },
            ],
          },
        },
        hourly: { status: "unavailable" },
        alerts: { status: "fresh", data: { alerts: [] } },
      },
    },
  },
  assistant_weather_alerts: {
    status: {
      config: { revision: 2, enabled: true, poll_seconds: 300 },
      coverage: "degraded",
      location,
      notification_history: {
        total: 2,
        next_offset: 2,
        has_more: false,
        events: [
          {
            kind: "new",
            delivered_at: "2026-11-04T10:00:00+08:00",
            alert: {
              area: "浦东新区",
              hazard: "大风",
              level: "蓝色",
              published_at: "2026-11-04T09:00:00+08:00",
              body: "请关注大风影响",
              url: "https://example.com/alert",
            },
          },
          {
            kind: "cancel",
            delivered_at: "2026-11-03T12:00:00+08:00",
            alert: {
              area: "黄浦区",
              hazard: "雷电",
              level: "黄色",
              published_at: "2026-11-03T11:00:00+08:00",
              body: "预警已解除",
            },
          },
        ],
      },
      records: [
        {
          state: "not_reconfirmed",
          applicable: false,
          record: {
            alert: {
              area: "上海",
              hazard: "大风",
              level: "蓝色",
              body: "请关注大风影响",
              published_at: "2026-11-04T09:00:00+08:00",
              url: "https://example.com/alert",
            },
          },
        },
      ],
    },
  },
  assistant_github: {
    status: {
      config: {
        revision: 2,
        login: "demo",
        enabled: true,
        auto_discover: true,
        poll_seconds: 60,
        discovery_seconds: 300,
      },
      watches: [
        {
          id: "watch1",
          revision: 3,
          url: "https://github.com/example/repo/pull/1",
          repository: "example/repo",
          number: 1,
          enabled: true,
          baseline: {
            title: "修复项目配置加载",
            state: "open",
            ci: { failed: [] },
          },
        },
      ],
    },
    query: {
      title: "修复项目配置加载",
      state: "open",
      fetched_at: "2026-11-04T10:00:00+08:00",
      items: { comment: { author: "reviewer", body: "请检查边界条件。" } },
    },
  },
  assistant_ai_news: {
    latest: {
      message: "近期 AI 简报\n一项新的模型推理研究公布了实验结果。",
      snapshot: { id: "news1" },
      output: { events: [] },
    },
    list: {
      id: "news1",
      candidates: [
        {
          id: "article1",
          title: "Inference research update",
          sources: ["研究团队"],
          published_at: "2026-11-04T09:00:00Z",
        },
      ],
    },
    read: {
      articles: [
        {
          title: "Inference research update",
          url: "https://example.com/research",
          text: "这是测试使用的新闻正文片段。",
        },
      ],
    },
  },
  assistant_morning: {
    latest: {
      message: "早上好。今天有编译原理课程，别忘了检查课程报告。",
      snapshot: { date: "2026-11-04" },
    },
  },
  assistant_evening: {
    latest: {
      message: "今天还有一件待办未完成，明天记得继续处理。",
      snapshot: { date: "2026-11-03" },
    },
  },
  assistant_proposals: {
    list: [
      {
        id: "proposal1",
        revision: 1,
        candidate: {
          title: "提交课程报告",
          due_date: "2099-11-06",
          evidence_quote: "请于周五前提交报告。",
        },
        question: "要把这件事加入待办吗？",
      },
    ],
  },
  assistant_server_health: {
    status: {
      status: "ok",
      collected_at: "2026-11-04T10:00:00+08:00",
      items: {
        disk: { status: "ok", value: { free_bytes: 21e9, total_bytes: 59e9 } },
        memory: {
          status: "ok",
          value: { available_bytes: 2e9, total_bytes: 4e9 },
        },
        load: {
          status: "ok",
          value: { averages: [0.2, 0.3, 0.4], cpu_count: 2 },
        },
        website: { status: "ok", value: { http_status: 200 } },
        friday: { status: "ok", value: "运行中" },
        asrobot_container: { status: "unknown", reason: "not_monitored" },
      },
    },
  },
};
const profile = {
  revision: "profile1",
  prompt: "你是一个私人助手。\n保留这段已有的规则。",
  model_ref: "demo/model1",
  providers: [{ name: "demo", default_effort: "medium" }],
};
function endpoint(path, body = {}) {
  if (path === "/me")
    return {
      user: { id: "test-owner", login: "演示用户", role: "user" },
      csrf: "test",
    };
  if (path === "/settings")
    return { native_mode: true, name: "小助", modules: {} };
  if (path === "/workspace")
    return { native_mode: true, instance_managed: true };
  if (path === "/native/tools")
    return Object.keys(fixtures).map((name) => ({
      name,
      schema: { properties: {} },
    }));
  if (path === "/native/status")
    return { running: true, model_ref: "demo/model1" };
  if (path === "/native/identity")
    return { revision: "identity1", name: "小助", address: "同学" };
  if (path === "/native/profile") return profile;
  if (path === "/native/mail-accounts")
    return { revision: 1, accounts: [], proxy_available: false };
  if (path === "/native/weather-setup") return { configured: false };
  if (path === "/native/github-credential")
    return { configured: false, revision: 1 };
  if (path === "/native/mail-test") return [{ id: "qq", authenticated: true }];
  if (path === "/activity")
    return [{ event: "登录", status: "success", created: 1793750400 }];
  if (path === "/wechat") return { running: true, wechat_configured: true };
  if (path === "/conversations")
    return { conversations: [], workspace: { instance_managed: true } };
  if (path.startsWith("/messages?")) return { messages: [], has_more: false };
  if (path === "/native/tool") {
    const result = fixtures[body.tool]?.[body.args?.action];
    return result === undefined
      ? { ok: true, reminder_date: "2099-11-24" }
      : result;
  }
  return { ok: true };
}
module.exports = { fixtures, endpoint, profile };
