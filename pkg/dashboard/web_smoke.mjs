// A smoke test for the dashboard's JavaScript, run by TestWebSmoke.
//
// The Go tests drive the API and can prove the bundle is served, but none of
// them executes a line of it. This does four things: loads every module, which
// catches a syntax error or a mistyped import; checks the schedule paraphrase
// and the other formatting; checks that model output is inserted as text rather
// than as markup; and renders all five screens against payloads copied from
// what the real handlers return - which is what catches a screen reading a
// field the API does not send, since it renders with the value simply missing.
//
// The screens reach the server through fetch, so stubbing fetch is enough to
// drive them; no module mocking is involved. The DOM stub covers only what
// dom.js touches. It is not a browser and is not trying to be one - the things
// it cannot see are layout, styling and anything that needs a real event loop.
import { readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { pathToFileURL } from "node:url";

const root = join(process.argv[2] ?? ".", "web");

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

const files = walk(root).filter((f) => f.endsWith(".js"));

// --- minimal DOM ------------------------------------------------------------
class Node {}
class Element extends Node {
  constructor(tag) {
    super();
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this.textContent = "";
    this.className = "";
    this.style = "";
  }
  setAttribute(name, value) {
    this.attributes[name] = value;
  }
  addEventListener(name, fn) {
    (this.listeners[name] ||= []).push(fn);
  }
  append(...nodes) {
    this.children.push(...nodes);
  }
  replaceChildren(...nodes) {
    this.children = nodes;
  }
  focus() {}
  get text() {
    const own = this.textContent || "";
    return own + this.children.map((c) => (c.text !== undefined ? c.text : c.data || "")).join("");
  }
}
globalThis.Node = Node;
globalThis.document = {
  createElement: (tag) => new Element(tag),
  createTextNode: (data) => ({ data, text: data }),
  getElementById: () => new Element("div"),
  body: new Element("body"),
};
globalThis.window = { location: { hash: "" }, addEventListener() {}, scrollTo() {} };

// --- a stand-in API ---------------------------------------------------------
// The screens reach the server through fetch, so serving fetch is enough to
// render them for real - no module mocking, and the payloads below are copies
// of what the Go handlers actually returned when this was written.
const RUN = {
  id: "run-1",
  agent_id: "agent-1",
  schedule_id: null,
  trigger: "manual",
  status: "failed",
  scheduled_for: "2026-09-10T11:00:00Z",
  picked_at: "2026-09-10T11:00:01Z",
  started_at: "2026-09-10T11:00:01Z",
  finished_at: "2026-09-10T11:00:44Z",
  output: "",
  error: "the model returned 401",
  prompt_tokens: 900,
  completion_tokens: 120,
  rendered_prompt: "Summarise the day.",
  created_at: "2026-09-10T11:00:00Z",
};

const AGENT = {
  id: "agent-1",
  name: "Morning brief",
  description: "calendar + goals",
  model: "gpt-4o-mini",
  base_url: "https://api.openai.com/v1",
  system_prompt: "You are terse.",
  user_prompt: "Summarise the day.",
  tools: ["http_fetch", "retired_tool"],
  max_steps: 12,
  max_tokens: 50000,
  max_duration_seconds: 300,
  secret_name: "default",
  context_mode: "last_n",
  context_runs: 3,
  enabled: true,
  created_at: "2026-09-10T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};

const SCHEDULE = {
  id: "schedule-1",
  agent_id: "agent-1",
  cron_expression: "0 7 * * *",
  timezone: "Europe/London",
  enabled: true,
  next_fire_at: "2026-09-11T06:00:00Z",
  last_fired_at: null,
  created_at: "2026-09-10T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};

const responses = {
  "/contract.json": { types: {} },
  "/api/session": { signed_in: true },
  "/api/agents": [{ ...AGENT, schedule: SCHEDULE, last_run: RUN }],
  "/api/agents/agent-1": AGENT,
  "/api/agents/agent-1/schedule": SCHEDULE,
  "/api/agents/agent-1/runs": [RUN],
  "/api/agents/agent-1/memory": [
    {
      id: "memory-1",
      agent_id: "agent-1",
      namespace: "gym",
      key: "last_split",
      content: { split: "push" },
      created_at: "2026-09-09T10:00:00Z",
    },
  ],
  "/api/runs/run-1": RUN,
  "/api/runs/run-1/steps": [
    {
      id: "step-1",
      step_index: 0,
      kind: "tool_call",
      content: "",
      tool_name: "http_fetch",
      arguments: '{"url":"https://example.com"}',
      result: '{"status":200}',
      error: "",
      prompt_tokens: 0,
      completion_tokens: 0,
      created_at: "2026-09-10T11:00:10Z",
    },
  ],
  "/api/tools": [{ name: "http_fetch", description: "Fetch a URL." }],
  "/api/secrets": [
    {
      id: "secret-1",
      name: "default",
      masked_suffix: "…3456",
      created_at: "2026-09-10T10:00:00Z",
      updated_at: "2026-09-10T10:00:00Z",
    },
  ],
  "/api/schedules/preview": { fire_times: ["2026-09-11T06:00:00Z"] },
};

const unanswered = [];
globalThis.fetch = async (path) => {
  const body = responses[path];
  if (body === undefined) unanswered.push(path);
  return {
    ok: true,
    status: 200,
    statusText: "OK",
    json: async () => body ?? {},
    text: async () => JSON.stringify(body ?? {}),
  };
};

let failures = 0;

// --- 1. every module parses and loads --------------------------------------
for (const file of files) {
  const name = relative(root, file);
  if (name === "app.js") continue; // boots the whole dashboard; covered below
  try {
    await import(pathToFileURL(file).href);
    console.log(`load  ok    ${name}`);
  } catch (error) {
    failures++;
    console.log(`load  FAIL  ${name}: ${error.message}`);
  }
}

// --- 2. the pure logic actually behaves ------------------------------------
const fmt = await import(pathToFileURL(join(root, "format.js")).href);

const cases = [
  ["0 7 * * *", "Europe/London", "Daily at 07:00 (Europe/London)"],
  ["*/15 * * * *", "UTC", "Every 15 minutes (UTC)"],
  ["30 * * * *", "UTC", "Hourly at 30 past (UTC)"],
  ["0 16 * * 1-5", "UTC", "Weekdays at 16:00 (UTC)"],
  ["0 9 * * 1", "UTC", "Every Monday at 09:00 (UTC)"],
  ["0 0 1 * *", "UTC", "Monthly on day 1 at 00:00 (UTC)"],
  ["15 3 */2 * *", "UTC", "15 3 */2 * *"], // no paraphrase: shows what was typed
];
for (const [expression, zone, want] of cases) {
  const got = fmt.describeCron(expression, zone);
  const ok = got === want;
  if (!ok) failures++;
  console.log(`cron  ${ok ? "ok   " : "FAIL "} ${expression} -> ${got}`);
}

const checks = [
  ["statusTone succeeded", fmt.statusTone("succeeded"), "ok"],
  ["statusTone budget_exceeded", fmt.statusTone("budget_exceeded"), "bad"],
  ["statusTone running", fmt.statusTone("running"), "busy"],
  ["statusWord budget_exceeded", fmt.statusWord("budget_exceeded"), "budget exceeded"],
  ["statusWord missing", fmt.statusWord(null), "never run"],
  ["isActive pending", fmt.isActive("pending"), true],
  ["isActive failed", fmt.isActive("failed"), false],
  ["duration", fmt.duration("2026-01-01T00:00:00Z", "2026-01-01T00:01:05Z"), "1m 5s"],
  ["duration unfinished", fmt.duration("2026-01-01T00:00:00Z", null), "—"],
  ["tokens", fmt.tokens({ prompt_tokens: 1200, completion_tokens: 300 }), (1500).toLocaleString()],
  ["tokens none", fmt.tokens({}), "—"],
  ["timestamp missing", fmt.timestamp(null), "—"],
];
for (const [name, got, want] of checks) {
  const ok = got === want;
  if (!ok) failures++;
  console.log(`fmt   ${ok ? "ok   " : "FAIL "} ${name} -> ${JSON.stringify(got)}`);
}

// --- 3. dom.js escapes rather than interpolates ----------------------------
const dom = await import(pathToFileURL(join(root, "dom.js")).href);
const hostile = dom.el("pre", { text: "<script>alert(1)</script>" });
const asText = hostile.textContent === "<script>alert(1)</script>" && hostile.children.length === 0;
if (!asText) failures++;
console.log(`dom   ${asText ? "ok   " : "FAIL "} markup in model output stays text`);

const withChild = dom.el("div", { class: "row" }, ["a", dom.el("span", { text: "b" })]);
const built = withChild.className === "row" && withChild.children.length === 2;
if (!built) failures++;
console.log(`dom   ${built ? "ok   " : "FAIL "} children and class`);

// --- 4. every screen renders against realistic payloads ---------------------
// This is what catches a screen reading a field the API does not send: it
// renders, and the value it was meant to show is simply absent from the output.
const load = (name) => import(pathToFileURL(join(root, name)).href);
const { loadContract } = await load("api.js");
await loadContract();

async function render(module, fn, ...args) {
  const container = new Element("main");
  const view = { container, live: true };
  await (await load(module))[fn](view, ...args);
  return container.text;
}

const screens = [
  {
    name: "agents",
    text: await render("screens/agents.js", "renderAgents"),
    // The schedule in human form, the status word, and the failure the row
    // exists to make impossible to miss.
    wants: ["Morning brief", "Daily at 07:00 (Europe/London)", "failed", "Last run"],
  },
  {
    name: "runs",
    text: await render("screens/runs.js", "renderRuns", "agent-1"),
    wants: ["Morning brief", "Run now", "failed", "Triggered by hand", "took 43s", "1,020 tokens"],
  },
  {
    name: "run",
    text: await render("screens/run.js", "renderRun", "run-1"),
    // The trace, top to bottom, on one page.
    wants: ["Prompt as sent", "Summarise the day.", "http_fetch", "arguments", "result", "Output", "the model returned 401"],
  },
  {
    name: "agent",
    text: await render("screens/agent.js", "renderAgent", "agent-1"),
    // Including a grant the installation no longer offers, which must stay
    // visible rather than silently vanishing from the form.
    wants: ["Prompts", "Schedule", "Budgets", "Memory", "http_fetch", "retired_tool", "not available on this installation", "gym / last_split"],
  },
  {
    name: "settings",
    text: await render("screens/settings.js", "renderSettings"),
    wants: ["Credentials", "Model provider key", "set …3456", "Telegram bot token", "not set", "Tools this installation offers"],
  },
];

for (const screen of screens) {
  for (const want of screen.wants) {
    const ok = screen.text.includes(want);
    if (!ok) failures++;
    console.log(`screen ${ok ? "ok   " : "FAIL "} ${screen.name}: ${want}`);
  }
}

// A screen asking for an endpoint nobody serves is a screen that will render
// empty against the real API.
for (const path of new Set(unanswered)) {
  failures++;
  console.log(`screen FAIL  a screen fetched an unknown path: ${path}`);
}

console.log(failures === 0 ? "\nALL OK" : `\n${failures} FAILURES`);
process.exit(failures === 0 ? 0 : 1);
