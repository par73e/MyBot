const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { JSDOM } = require("jsdom");
const { endpoint, fixtures } = require("./robot-fixtures.cjs");
const staticDir = path.resolve(__dirname, "../app/static");
async function browser(override, url = "http://localhost/asrobot/", storage = {}) {
  const dom = new JSDOM('<div id="notice"></div><div id="root"></div>', {
      url,
      runScripts: "outside-only",
    }),
    w = dom.window,
    calls = [];
  for (const [key,value] of Object.entries(storage)) w.sessionStorage.setItem(key,value);
  w.HTMLElement.prototype.scrollIntoView = function () {};
  w.scrollTo = () => {};
  w.confirm = () => true;
  w.structuredClone = structuredClone;
  w.fetch = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : {},
      p = String(url).replace("/asrobot/api", "");
    calls.push({ path: p, body, method: options.method || "GET" });
    return {
      ok: true,
      json: async () =>
        structuredClone(override ? override(p, body) : endpoint(p, body)),
    };
  };
  for (const f of ["native.js", "robot-pages.js", "app.js"])
    vm.runInContext(
      fs.readFileSync(path.join(staticDir, f), "utf8"),
      dom.getInternalVMContext(),
      { filename: f },
    );
  await new Promise((r) => setTimeout(r, 15));
  return {
    w,
    calls,
    close: () => w.close(),
    el: () => w.document.querySelector("#content"),
  };
}
async function submit(w, form) {
  form.dispatchEvent(
    new w.SubmitEvent("submit", {
      bubbles: true,
      cancelable: true,
      submitter: form.querySelector("button[type=submit],button:not([type])"),
    }),
  );
  await new Promise((r) => setTimeout(r, 15));
}
test("every module and top-level page renders dedicated user content without raw tool forms", async () => {
  const b = await browser();
  try {
    for (const tool of Object.keys(fixtures).filter(
      (t) => !["assistant_location", "assistant_academic"].includes(t),
    )) {
      await b.w.eval(`robotNavigate('${tool}')`);
      assert.ok(!b.el().querySelector(".inline-error"), tool + " error");
      assert.ok(
        !b
          .el()
          .querySelector(
            ".native-tree,.native-json-input,[name=revision],[name=id],#native-action",
          ),
        tool + " leaks internals",
      );
      assert.ok(b.el().querySelector("h1"), tool);
    }
    for (const page of [
      "assistant",
      "model",
      "wechat",
      "history",
      "activity",
      "account",
    ]) {
      await b.w.eval(`go('${page}')`);
      assert.ok(b.el().querySelector("h1"), page);
    }
  } finally {
    b.close();
  }
});
test("task editor owns record revision, converts Shanghai time and separates completion", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_tasks')");
    b.el().querySelector("[data-task-edit=task1]").click();
    const f = b.el().querySelector("#robot-task-form");
    f.elements.title.value = "提交更新后的课程报告";
    f.elements.due_at.value = "2099-11-07T18:30";
    await submit(b.w, f);
    const call = b.calls.findLast((c) => c.body.args?.action === "update");
    assert.deepEqual(call.body.args, {
      action: "update",
      title: "提交更新后的课程报告",
      body: "检查附件后提交到课程平台",
      deadline_reminders: true,
      id: "task1",
      revision: 4,
      due_at: "2099-11-07T18:30:00+08:00",
    });
    b.el().querySelector("[data-task-action=complete][data-id=task2]").click();
    await new Promise((r) => setTimeout(r, 15));
    assert.equal(
      b.calls.findLast((c) => c.body.args?.action === "complete").body.args
        .occurrence_only,
      true,
    );
  } finally {
    b.close();
  }
});
test("weekly save keeps all seven days and supports midnight without overwriting triggers", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_heartbeat')");
    const f = b.el().querySelector("#routine-form");
    await submit(b.w, f);
    const args = b.calls.findLast((c) => c.body.args?.action === "set_weekly")
      .body.args;
    assert.equal(args.revision, 7);
    assert.equal(Object.keys(args.weekly_schedule).length, 7);
    assert.equal(args.weekly_schedule.monday.end, "24:00");
    assert.equal(args.triggers, undefined);
    b.el().querySelector('[data-trigger-edit="0"]').click();
    const trigger = b.el().querySelector("#trigger-form");
    trigger.elements.title.value = "我的晨报";
    await submit(b.w, trigger);
    const change = b.calls.findLast(
      (c) => c.body.args?.action === "set_trigger",
    ).body.args;
    assert.equal(change.trigger_id, "morning");
    assert.equal(change.modules, undefined);
    assert.equal(change.clock, undefined);
  } finally {
    b.close();
  }
});
test("preferences preserve original prompt; unsafe links and external text stay inert", async () => {
  const b = await browser();
  try {
    await b.w.eval("go('assistant')");
    const f = b.el().querySelector("#robot-preferences");
    f.elements.preferences.value = "先给结论。";
    await submit(b.w, f);
    const prompt = b.calls.findLast(
      (c) => c.path === "/native/profile" && c.method === "POST",
    ).body.prompt;
    assert.ok(prompt.startsWith("你是一个私人助手。\n保留这段已有的规则。"));
    assert.match(prompt, /先给结论。/);
    assert.equal(b.w.eval("robotLink('javascript:alert(1)','打开')"), "打开");
    await b.w.eval("robotNavigate('assistant_mail')");
    b.el().querySelector("#mail-recent").click();
    await new Promise((r) => setTimeout(r, 15));
    b.el().querySelector("[data-mail-read]").click();
    await new Promise((r) => setTimeout(r, 15));
    assert.equal(b.el().querySelector("#mail-body script"), null);
    assert.match(
      b.el().querySelector("#mail-body").textContent,
      /<script>unsafe/,
    );
  } finally {
    b.close();
  }
});
test("suggestion actions prepare a draft and never approve or send autonomously", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_proposals')");
    b.el().querySelector("[data-robot-draft]").click();
    await new Promise((r) => setTimeout(r, 15));
    assert.match(
      b.el().querySelector("#chat-form [name=text]").value,
      /提交课程报告/,
    );
    assert.equal(
      b.calls.some(
        (c) =>
          c.path === "/chat" ||
          ["approve", "reject"].includes(c.body.args?.action),
      ),
      false,
    );
  } finally {
    b.close();
  }
});
test("course editor preserves existing exceptions and sends typed configuration", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_courses')");
    b.el().querySelector("#course-edit").click();
    const f = b.el().querySelector("#course-config");
    f.querySelector("[data-course] [name=weeks]").value = "1-8,10";
    await submit(b.w, f);
    const args = b.calls.findLast((c) => c.body.args?.action === "import").body
      .args;
    assert.equal(args.revision, 3);
    assert.deepEqual(
      args.config.courses[0].weeks,
      [1, 2, 3, 4, 5, 6, 7, 8, 10],
    );
    assert.equal(args.config.courses[0].id, "course1");
    assert.deepEqual(args.config.exceptions, []);
  } finally {
    b.close();
  }
});

test("returning to all functions clears the selected module", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_tasks')");
    b.el().querySelector("[data-go=native]").click();
    await new Promise((r) => setTimeout(r, 15));
    assert.equal(b.el().querySelector("h1").textContent, "功能模块");
    assert.ok(b.el().querySelector(".robot-directory"));
  } finally {
    b.close();
  }
});
test("unconfigured mailbox keeps connection entry and weather failure is not presented as clear weather", async () => {
  const b = await browser((p, body) => {
    if (
      p === "/native/tool" &&
      ["assistant_mail", "assistant_weather"].includes(body.tool)
    )
      throw Error("not configured");
    return endpoint(p, body);
  });
  try {
    await b.w.eval("robotNavigate('assistant_mail')");
    assert.ok(b.el().querySelector("#mail-connect"));
    await b.w.eval("robotNavigate('assistant_weather')");
    assert.ok(b.el().querySelector("#weather-connect"));
    assert.match(b.el().textContent, /天气暂时不可用/);
  } finally {
    b.close();
  }
});
test("derived course schedule chooses only the course service and omits fixed clocks", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_heartbeat')");
    b.el().querySelector("#trigger-new").click();
    const f = b.el().querySelector("#trigger-form");
    f.elements.title.value = "上课前提醒";
    f.elements.mode.value = "course";
    f.elements.mode.dispatchEvent(new b.w.Event("change"));
    f.elements.lead.value = "15";
    await submit(b.w, f);
    const args = b.calls.findLast((c) => c.body.args?.action === "set_trigger")
      .body.args;
    assert.equal(args.mode, "course");
    assert.deepEqual(args.modules, ["course_reminder"]);
    assert.equal(args.lead_minutes, 15);
    assert.equal(args.clock, undefined);
    assert.equal(args.interval_seconds, undefined);
  } finally {
    b.close();
  }
});

test("warning page filters current districts but preserves notifications sent in previous locations", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_weather_alerts')");
    const panels = b.el().querySelectorAll("section.panel");
    assert.doesNotMatch(panels[0].textContent, /上海 大风/);
    assert.match(b.el().textContent, /13|共记录 2 次发送/);
    assert.match(b.el().textContent, /黄浦区 雷电黄色/);
    assert.match(b.el().textContent, /周日 17:00/);
    assert.match(b.el().textContent, /周五 19:00/);
  } finally {
    b.close();
  }
});
test("temporary itinerary is owner scoped, typed and includes the current revision", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_weather_alerts')");
    const f = b.el().querySelector("#location-plan-form");
    f.elements.place_id.value = "home";
    f.elements.label.value = "这周提前回家";
    f.elements.starts_at.value = "2099-11-06T12:00";
    f.elements.ends_at.value = "2099-11-08T17:00";
    await submit(b.w, f);
    const args = b.calls.findLast(
      (c) =>
        c.body.tool === "assistant_location" &&
        c.body.args?.action === "set_plan",
    ).body.args;
    assert.equal(args.revision, 3);
    assert.equal(args.place_id, "home");
    assert.equal(args.starts_at, "2099-11-06T12:00:00+08:00");
    assert.equal(args.ends_at, "2099-11-08T17:00:00+08:00");
    assert.equal(args.user_id, undefined);
  } finally {
    b.close();
  }
});

test("academic calendar shows day decisions and the active teaching-day reminder without raw data", async () => {
  const b = await browser();
  try {
    await b.w.eval("robotNavigate('assistant_courses')");
    assert.match(b.el().textContent, /今天是教学日/);
    assert.match(b.el().textContent, /10:55/);
    assert.match(b.el().textContent, /2027 年完整放假调休安排尚未核实/);
    assert.ok(
      b.el().querySelector('a[href="https://xxgk.shiep.edu.cn/502/list.htm"]'),
    );
    assert.doesNotMatch(b.el().textContent, /holiday_years|schoolday_reminder/);
  } finally {
    b.close();
  }
});

test("health shows activity counts and omits unmonitored Docker, including legacy snapshots", async () => {
  const b = await browser((p, body) => {
    const result = endpoint(p, body);
    if (result && result.items) result.items.asrobot_container = {status: 'unknown', reason: 'not_monitored'};
    return result;
  });
  try {
    await b.w.eval("robotNavigate('assistant_server_health')");
    const text = b.el().textContent;
    assert.match(text, /SSH 安全防护/);
    assert.match(text, /1407 次请求/);
    assert.match(text, /独立域名未启用访问日志/);
    assert.doesNotMatch(text, /Docker|容器|总体：|\[object Object\]/);
  } finally {b.close();}
});

test("refresh restores top-level and native routes, and browser history follows navigation", async () => {
  const b = await browser();
  try {
    await b.w.eval("go('history')");
    assert.equal(b.w.location.hash, "#/history");
    const reloaded = await browser(undefined, b.w.location.href);
    try { assert.equal(reloaded.w.eval("page"), "history"); assert.equal(reloaded.w.document.body.className, "page-history"); } finally { reloaded.close(); }
    await b.w.eval("robotNavigate('assistant_ai_news')");
    assert.equal(b.w.location.hash, "#/native/assistant_ai_news");
    const news = await browser(undefined, b.w.location.href);
    try { assert.equal(news.w.eval("selectedNativeModule"), "assistant_ai_news"); assert.ok(news.el().textContent.includes("最近一期简报")); } finally { news.close(); }
    b.w.history.back();
    await new Promise(r=>setTimeout(r,40));
    assert.equal(b.w.eval("page"), "history");
  } finally { b.close(); }
});
test("invalid routes are bounded and must-change accounts stay on the account page", async () => {
  for(const hash of ["#/bad", "#/native/not-a-tool"]){
    const b=await browser(undefined,"http://localhost/asrobot/"+hash);
    try { assert.equal(b.w.eval("selectedNativeModule"),null);assert.ok(!b.el().querySelector(".inline-error")); }finally{b.close();}
  }
  const b=await browser((p,body)=>p==="/me"?{user:{...endpoint(p,body).user,must_change:true},csrf:"test"}:endpoint(p,body),"http://localhost/asrobot/#/history");
  try{assert.equal(b.w.eval("page"),"account");assert.equal(b.w.location.hash,"#/account");}finally{b.close();}
});
test("latest news labels each origin and generation time without exposing raw fields", async () => {
  for(const [source,label] of Object.entries({weixin:"微信主动查询",web:"网页主动查询",scheduled:"自动播报",morning:"早安播报"})){
    const b=await browser((p,body)=>{
      const result=endpoint(p,body);
      if(p==="/native/tool"&&body.tool==="assistant_ai_news"&&body.args?.action==="latest")return {...result,source,generated_at:"2026-11-07T01:20:00Z"};
      return result;
    });
    try{await b.w.eval("robotNavigate('assistant_ai_news')");assert.ok(b.el().textContent.includes(label),b.el().textContent);assert.ok(b.el().textContent.includes("2026"));}finally{b.close();}
  }
});

test("a temporary module-catalog failure preserves the requested route", async()=>{
  const b=await browser((p,body)=>{if(p==="/native/tools")throw Error("temporary failure");return endpoint(p,body)},"http://localhost/asrobot/#/native/assistant_ai_news");
  try{assert.equal(b.w.location.hash,"#/native/assistant_ai_news");assert.equal(b.w.eval("selectedNativeModule"),"assistant_ai_news");}finally{b.close();}
});
test("news read failures are shown as failures rather than an empty report history",async()=>{
  const b=await browser((p,body)=>{if(p==="/native/tool"&&body.tool==="assistant_ai_news"&&body.args?.action==="latest")throw Error("temporary failure");return endpoint(p,body)});
  try{await b.w.eval("robotNavigate('assistant_ai_news')");assert.ok(b.el().textContent.includes("暂时无法读取最近简报"));assert.ok(!b.el().textContent.includes("暂时没有完整简报"));}finally{b.close();}
});

test("reload restores sidebar expansion and show-all state for the same account", async()=>{
  const b=await browser();
  try{
    b.w.document.querySelector(".module-toggle").click();
    b.w.document.querySelector("[data-module-more]").click();
    await b.w.eval("robotNavigate('assistant_ai_news')");
    const key="asrobot:navigation:test-owner";
    const storage={[key]:b.w.sessionStorage.getItem(key)};
    const reloaded=await browser(undefined,b.w.location.href,storage);
    try{
      assert.equal(reloaded.w.document.querySelector(".module-toggle").getAttribute("aria-expanded"),"true");
      assert.equal(reloaded.w.document.querySelector("[data-module-more]").getAttribute("aria-expanded"),"true");
      assert.ok(reloaded.w.document.querySelector('[data-module-native="assistant_ai_news"]'));
      assert.equal(reloaded.w.eval("selectedNativeModule"),"assistant_ai_news");
      reloaded.w.document.querySelector(".module-toggle").click();
      const closed=await browser(undefined,reloaded.w.location.href,{[key]:reloaded.w.sessionStorage.getItem(key)});
      try{assert.equal(closed.w.document.querySelector(".module-toggle").getAttribute("aria-expanded"),"false");}finally{closed.close();}
    }finally{reloaded.close();}
  }finally{b.close();}
});
test("sidebar preferences are account scoped and tolerate malformed stored state", async()=>{
  for(const storage of [
    {"asrobot:navigation:someone-else":JSON.stringify({open:true,all:true})},
    {"asrobot:navigation:test-owner":"not-json"},
    {"asrobot:navigation:test-owner":JSON.stringify({open:"true",all:"true"})}
  ]){
    const b=await browser(undefined,"http://localhost/asrobot/",storage);
    try{assert.equal(b.w.eval("moduleMenuOpen"),false);assert.equal(b.w.eval("moduleMenuAll"),false);assert.ok(!b.el().querySelector(".inline-error"));}finally{b.close();}
  }
});

test("a deep-linked module is revealed until the account explicitly chooses a collapsed menu",async()=>{
  const b=await browser(undefined,"http://localhost/asrobot/#/native/assistant_ai_news");
  try{assert.equal(b.w.eval("moduleMenuOpen"),true);assert.equal(b.w.eval("moduleMenuAll"),true);assert.ok(b.w.document.querySelector('aside nav [data-module-native="assistant_ai_news"]'));}finally{b.close();}
});
