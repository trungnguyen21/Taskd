# PRD — Taskd: Scheduled AI Agents

## Problem Statement

I have recurring work that a language model could do for me, but nothing to run it while I'm asleep.

Every morning I want a briefing that pulls my calendar together with the goals I set for myself. Every afternoon I want a market sentiment digest built from the news that broke that day. At 4pm I want to know which gym split I'm on and what I lifted last time. Each of these is a prompt I could write in thirty seconds — and each is worthless unless something runs it, unattended, on a schedule, forever.

The tools that exist don't fit:

- **Chat assistants with scheduled tasks** run the prompt but are a black box. I can't choose the model, I can't see why it produced what it did, its memory is opaque and I can't write to it on a schedule, and my data lives on someone else's server.
- **Workflow automation tools** connect services beautifully but treat the model as one node in a graph I have to draw by hand. I don't want to draw a graph. I want to describe a job.
- **Writing it myself** means a cron entry, a script, an API client, a retry story, a secrets file, and a notification path — per agent. I've done it. The second agent is as much work as the first, and when one silently stops working I find out three weeks later.

Underneath all of it: an agent that runs unattended, spends my money, and messages me is only worth having if I can **see what it did** and **trust it won't run away**. Nothing gives me both.

## Solution

**Taskd is a self-hosted scheduler for AI agents.** I run it with one `docker compose up`. In its dashboard I define an agent — pick a model, write a prompt, grant it tools, decide whether it gets memory — and attach a cron schedule. From then on Taskd runs it on that interval and sends the result to my Telegram.

It is not a chat app and not a workflow builder. It is **cron for agents**.

What makes it worth running instead of the alternatives:

- **My model, my key.** Anthropic, OpenAI, Gemini, or anything OpenAI-compatible — including Ollama on the box next to it.
- **My box.** Nothing leaves my infrastructure except the model calls I configured.
- **Every run is inspectable.** The exact prompt that was sent, every tool call with its arguments and its result, tokens consumed. When an agent does something strange at 6am I can answer *why* three weeks later.
- **Agents remember.** An agent can write records that later runs read back, so a scheduled job can reason about what changed rather than re-observing the world from scratch every day.
- **It cannot run away.** Every run has an enforced ceiling on steps, tokens, and wall-clock time.
- **It tells me when it breaks.** A failing agent can't report its own failure, so the platform does.

## User Stories

### Defining an agent

1. As a self-hoster, I want to create an agent from the dashboard at runtime, so that I can add a new scheduled job without editing config files or redeploying.
2. As a self-hoster, I want to give each agent a name and description, so that I can tell my agents apart six months later.
3. As a self-hoster, I want to write an agent's system prompt and user prompt separately, so that I can keep durable instructions apart from the specific request.
4. As a self-hoster, I want to choose which model an agent uses, so that I can run a cheap model for simple digests and a strong one for analysis.
5. As a self-hoster, I want to point an agent at a custom base URL, so that I can use Ollama or any other OpenAI-compatible endpoint running on my own hardware.
6. As a self-hoster, I want to select which tools an agent may use from a catalog, so that I control exactly what each agent can reach.
7. As a self-hoster, I want an agent with no tools granted to still run, so that a pure prompt-only agent is possible.
8. As a self-hoster, I want to edit an agent and have the change apply to its next run, so that I can iterate on a prompt without recreating anything.
9. As a self-hoster, I want to duplicate an existing agent, so that I can build a variant without retyping its configuration.
10. As a self-hoster, I want to disable an agent without deleting it, so that I can pause a job while I debug it.
11. As a self-hoster, I want to delete an agent and choose whether its run history goes with it, so that I can clean up without losing records I still want.

### Running an agent

12. As a self-hoster, I want the model to decide which of its granted tools to call and in what order, so that I can describe a goal rather than a procedure.
13. As a self-hoster, I want to trigger a run manually from the dashboard, so that I can test an agent immediately instead of waiting for its schedule.
14. As a self-hoster, I want to see a run's steps appear while it is still running, so that I can tell a slow agent from a stuck one.
15. As a self-hoster, I want to cancel a run in progress, so that I can stop an agent that is clearly going wrong before it spends more money.
16. As a self-hoster, I want each run to record the fully rendered prompt that was actually sent, so that I can see what the model saw rather than what I think I wrote.
17. As a self-hoster, I want each tool call recorded with its arguments and its result, so that I can find the exact step where a run went wrong.
18. As a self-hoster, I want token counts on every run, so that I have a real measure of what my agents consume before the provider bill tells me.
19. As a self-hoster, I want a run to end with a clear status — succeeded, failed, missed, cancelled, or budget-exceeded — so that I can scan history without reading every trace.
20. As a self-hoster, I want a failed run to record the error that caused it, so that I can distinguish a bad API key from a tool timeout.

### Budgets and safety

21. As a self-hoster, I want every new agent to come with sane step, token, and duration limits already applied, so that I am protected before I have thought about limits at all.
22. As a self-hoster, I want to raise or lower those limits per agent from the dashboard, so that a research agent can run longer than a reminder.
23. As a self-hoster, I want limits enforced by the platform rather than requested of the model, so that a confused agent cannot ignore them.
24. As a self-hoster, I want a run that hits a limit to stop and be clearly marked as such, so that I can tell a budget stop from a crash.
25. As a self-hoster, I want tools that fetch URLs to refuse internal and cloud-metadata addresses, so that an agent cannot be talked into attacking my own network.

### Scheduling

26. As a self-hoster, I want to attach a cron schedule to an agent, so that it runs on the cadence I choose.
27. As a self-hoster, I want to set the schedule's timezone, so that "7am" means 7am where I live and keeps meaning that across daylight saving changes.
28. As a self-hoster, I want to pick a schedule from a friendly picker instead of typing cron syntax, so that I don't have to remember field order.
29. As a self-hoster, I want to see the next few fire times for a schedule before I save it, so that I can confirm I got it right.
30. As a self-hoster, I want a run that was missed while the system was down to be recorded as missed and not executed late, so that I don't get yesterday's briefing at lunchtime or a burst of catch-up runs after an outage.
31. As a self-hoster, I want a run whose worker died to be marked failed rather than left running forever, so that the dashboard tells me the truth after a restart or a redeploy.
32. As a self-hoster, I want runs not to be retried automatically by default, so that an agent that sends messages doesn't send them twice.

### Memory

33. As a self-hoster, I want an agent to be able to write records it can read back on later runs, so that it can report what changed rather than describing the world afresh each time.
34. As a self-hoster, I want an agent to have memory only if I grant it the memory tools, so that a stateless agent is stateless by construction and not by a setting I might forget.
35. As a self-hoster, I want to browse and search what an agent has remembered, so that I can understand its behavior and correct it.
36. As a self-hoster, I want to edit or delete individual memory records, so that I can remove something wrong before it poisons every future run.
37. As a self-hoster, I want memory reads to be bounded in size, so that a long-lived agent's prompt doesn't grow until it costs ten times what it did at first.
38. As a self-hoster, I want a hard cap on how much an agent can store, so that memory can't grow without limit on my disk.

### Tools

39. As a self-hoster, I want a web search tool, so that agents can research topics I care about.
40. As a self-hoster, I want an HTTP fetch tool, so that agents can read specific pages, feeds, and APIs I point them at.
41. As a self-hoster, I want to supply my own search provider key, so that I control that cost and dependency.
42. As a self-hoster, I want to see exactly which tools a given agent is allowed to use on its detail page, so that "what can this thing touch" is answerable at a glance.

### Delivery and notification

43. As a self-hoster, I want agent output delivered to my Telegram, so that a job that runs while I sleep actually reaches me.
44. As a self-hoster, I want the dashboard to help me discover my Telegram chat id, so that setup doesn't require me to hand-call an API.
45. As a self-hoster, I want long output split or attached as a file, so that a digest isn't truncated by Telegram's message limit.
46. As a self-hoster, I want a webhook tool, so that I can route output to Discord, ntfy, Home Assistant, or anything else without waiting for an integration.
47. As a self-hoster, I want notification to be something the agent decides to do, so that I can build an agent that only messages me when something actually changed.
48. As a self-hoster, I want every run's output stored in an in-app inbox regardless, so that Taskd is useful before I've configured a bot and nothing is lost if an agent forgets to send.
49. As a self-hoster, I want unread run output marked in the dashboard, so that I can see at a glance what I haven't read.
50. As a self-hoster, I want to be notified when an agent fails repeatedly, so that a broken job doesn't sit silently for weeks.
51. As a self-hoster, I want failure notifications to come from the platform rather than the agent, so that I still hear about it when the agent's own model key is what's broken.

### Credentials and access

52. As a self-hoster, I want to store provider API keys in the dashboard, so that I don't have to redeploy to change a key.
53. As a self-hoster, I want keys encrypted at rest, so that a database dump doesn't hand over my provider account.
54. As a self-hoster, I want keys shown only as a masked suffix after saving, so that they can't be read back out of the UI.
55. As a self-hoster, I want to log in before I can see or change anything, so that an instance exposed to my network isn't open to everyone on it.
56. As a self-hoster, I want to validate a key when I save it, so that I learn it's wrong immediately rather than at 7am tomorrow.

### Operating the system

57. As a self-hoster, I want to install with a single `docker compose up`, so that I can evaluate Taskd in ten minutes.
58. As a self-hoster, I want prebuilt images, so that I don't have to compile Go to try it.
59. As a self-hoster, I want schema migrations to run automatically on startup, so that upgrading to a new version doesn't require me to apply SQL by hand.
60. As a self-hoster, I want configuration through environment variables, so that it fits how I already run everything else.
61. As an operator running Kubernetes, I want manifests for the services, so that I can run Taskd on my cluster.
62. As an operator, I want workers to finish their current run when asked to shut down, so that a rolling deploy doesn't kill work mid-flight.
63. As an operator, I want the coordinator to survive restarts and run more than one replica, so that the control plane isn't a single point of failure.
64. As an operator, I want to add workers to increase throughput, so that more agents can run concurrently.
65. As an operator, I want a health endpoint per service, so that my orchestrator can tell whether they're alive.

### Using the dashboard

66. As a self-hoster, I want the dashboard to open on a list of my scheduled agents, so that the first thing I see is whether my jobs are healthy.
67. As a self-hoster, I want each agent in that list to show its schedule, its status, and its last run at a glance, so that I can check on everything without clicking into anything.
68. As a self-hoster, I want a failing agent to be visually obvious in the list, so that a broken job cannot hide among working ones.
69. As a self-hoster, I want a dark interface, so that a dashboard I check at 7am and 11pm is comfortable to look at.
70. As a self-hoster, I want the interface plain and static, so that nothing animates, slides, or decorates while I am trying to read what my agents did.
71. As a self-hoster, I want to reach an agent's run history in one click from the list, so that investigating a bad result is fast.
72. As a self-hoster, I want a run's trace on a single scrollable page, so that I can read what happened top to bottom without expanding anything.
73. As a self-hoster, I want the agent form to be one page, so that creating an agent is filling in a form rather than completing a wizard.
74. As a self-hoster, I want configuration for my keys and my Telegram bot in one settings page, so that setup is finished in one place.

## Implementation Decisions

### Architecture

The three-service topology from the template is kept: an API service, a coordinator, and workers, communicating over gRPC with PostgreSQL as the system of record. Push-based dispatch and the worker heartbeat protocol are retained deliberately — they carry the distributed-systems value of the project, at the known cost of no scale-to-zero.

- **API service** (the current scheduler service, renamed and expanded) — serves the dashboard over REST/JSON and remains a gRPC client of the coordinator. GraphQL was considered and rejected: one client with fixed views doesn't repay a schema, resolvers, codegen, and an N+1 problem on the run→steps relation. A Connect/gRPC-Web bridge was also considered and rejected to avoid adding a proxy or codegen toolchain to a project whose adoption story is one-command install. The two boundaries are contracted independently — REST at the public edge because a browser must speak it, gRPC in the interior for typing, speed, and streaming — and the overlap between them is small (see API contract below).
- **Coordinator** — gains a **materializer** that expands schedules into concrete runs, and a **reaper** that fails runs whose lease has expired. Both run on the existing scan ticker. The existing `FOR UPDATE SKIP LOCKED` claim query is reused verbatim against the runs table.
- **Worker** — gains the **executor**: the agent tool-calling loop. This replaces the placeholder that currently just sleeps.
- **Dashboard** — a new service in the same repository and the same compose file, so one command brings up everything.

### Data model

New tables. Every table carries an owner column from the start, even though v1 has a single user, because retrofitting ownership later is a migration across the entire schema.

| Table | Purpose | Notes |
|---|---|---|
| `users` | Login and per-user settings | Single user in v1; Telegram config and failure-notification target live here |
| `agents` | Agent definition | Model, base URL, prompts, granted tool names, budgets, context mode, enabled flag |
| `schedules` | Cron recurrence | Cron expression plus IANA timezone; one per agent in v1 |
| `runs` | One execution | Status, timestamps, lease expiry, token counts, cost, final output, error |
| `run_steps` | Ordered trace | Step index, type, tool name, arguments, result, token counts |
| `memory_records` | Agent memory | Namespace, key, JSON content, created-at; append-only |
| `secrets` | Provider and tool credentials | Envelope-encrypted ciphertext plus masked display suffix |
| `workers` | Worker registry | Address and last-heartbeat; moved out of coordinator memory |

**Cost is recorded but not computed in v1.** Runs carry token counts, which are reported by the provider and always correct. The cost column exists and stays empty: converting tokens to money requires a per-model price table maintained by hand, which goes stale silently and would be displayed as fact. The dashboard shows tokens. Pricing can be added later without a schema change.

Existing schema corrections: timestamps become `TIMESTAMPTZ` (the current no-zone columns compared against `NOW()` break on the first DST shift), and the tasks table is superseded by runs.

**Memory namespacing.** Records are keyed by namespace, and the namespace defaults to the agent's own identifier. Behaviorally this is per-agent-private, which is what v1 wants; structurally it means cross-agent sharing later is a grant row rather than a schema change. An agent has memory if and only if the memory tools are among its granted tools — there is no separate memory flag.

**Run context.** An agent declares whether a run starts fresh or is seeded with the output of the previous N runs. Default is fresh, to avoid one bad run's output contaminating every run after it.

### Executor

A tool-calling loop: render the prompt, call the model, execute any requested tools, feed results back, repeat until the model answers or a budget is hit. Every iteration is persisted as a run step before the next begins, so a trace survives a crash mid-run.

Budgets — maximum steps, maximum tokens, maximum wall-clock duration — are enforced by the executor, not requested of the model. They are defaulted on every new agent, adjustable per agent, and bounded by a non-overridable ceiling.

Workers renew a lease on the run while executing. The coordinator's reaper fails runs whose lease has expired. This is required rather than optional given the Kubernetes target, where pods cycle routinely during rolling deploys and node scale-down.

Workers reject dispatch when saturated so the coordinator can route elsewhere. With runs measured in minutes rather than seconds, the current unbounded push would otherwise silently strand runs already marked as picked up.

### Model provider interface

**The executor speaks one wire format: OpenAI-compatible chat completions with tool calling.** This is not an endorsement of a provider — it is the de facto interchange format, and targeting it means a single request shape, a single tool-declaration shape, and a single tool-result shape covers OpenAI, Ollama, LM Studio, vLLM, OpenRouter, and, through their published compatibility endpoints, Anthropic and Gemini.

Alternatives considered and rejected. A per-provider native adapter set was rejected as the largest unpriced cost in the plan: three or more tool-calling dialects to write and keep current as each provider evolves independently. A Python worker built on LangGraph or Strands was rejected because it makes the repository polyglot and, more decisively, because its headline feature — durable checkpointed graph state — is something this system deliberately does not want: mid-run recovery is out of scope, and run state is persisted to purpose-built tables that back the trace viewer. Go agent frameworks were rejected as immature relative to the Python ecosystem and likely to fight that same schema. The loop itself is small enough to own.

The compatibility layers are migration aids rather than full-fidelity APIs, and gaps around provider-specific features are expected. A native adapter can be added later behind the same internal interface without changing the executor. Operators needing a provider outside this set can run a normalizing proxy in front of Taskd; this is documented as an option, never a dependency.

### Tolerating weak models

Local models are a first-class target and many of them are unreliable at tool calling: malformed arguments, invented tool names, schema violations, and repetition are normal rather than exceptional. The executor treats a malformed tool call as a recoverable step, not a crash — the error is recorded as a run step, fed back to the model as a tool result so it can correct itself, and counted against the step budget so a model that cannot recover terminates instead of looping.

A run that fails this way is marked failed with the reason preserved, so the user can see that the model was the problem rather than the tool. Documentation states plainly which local models have been observed to work, because the audience most attracted by Ollama support is the audience most likely to hit this.

### Tool registry

A closed first-party registry in v1: web search, HTTP fetch, memory read, memory write, send Telegram, send webhook. The set is deliberately capability-shaped rather than service-shaped — nothing named after a vendor, so nothing rots when a vendor changes an API and nothing needs OAuth.

The registry interface is **MCP-shaped**: a tool is a name, a description, a JSON Schema for its input, and a handler returning content blocks, registered into a catalog the dashboard enumerates. This is a deliberate hedge — adding MCP servers after v1 becomes an adapter that registers into the same catalog, rather than a rewrite of the executor, the tool picker, and the trace format.

URL-taking tools enforce egress rules blocking private ranges and cloud metadata endpoints.

### Scheduling semantics

- Cron expression plus IANA timezone, with a five-minute minimum granularity. Wall-clock intent plus zone is what allows the next instant to be recomputed correctly across DST.
- **Fire times are computed forward from the last fired instant.** The last fire is stored in UTC, the next candidate is computed as wall-clock time in the schedule's location and converted to UTC, and a candidate is accepted only if it is strictly later than the last fire. This single rule resolves both daylight-saving hazards: on the spring transition a schedule falling in the skipped hour has no valid instant and is simply not fired; on the autumn transition the repeated hour cannot fire twice, because the second occurrence is not strictly after the first. Without it, an agent granted message-sending and memory-writing tools sends twice and writes twice on one day a year.
- **The timezone database must be available in the container.** Timezone lookup reads the system database, which minimal base images do not carry — every schedule then fails at runtime while working correctly on a developer machine. Either the database is embedded in the binary or installed in the image, and this is verified in the container rather than locally.
- The chosen cron library's behavior across both transitions is verified by test rather than taken from its documentation.
- A run whose fire time has passed unexecuted beyond a grace window is recorded as missed and not run. This prevents both stale output and catch-up storms after an outage.
- Overlapping runs are not handled in v1; schedules are expected to be hours apart.
- No automatic retries. An agent granted message-sending and memory-writing tools is not idempotent. Manual re-run is always available.

### Secrets

Envelope encryption with a master key supplied by the environment. The envelope structure is in place from the start so that moving to a KMS later touches one component. Keys are write-only through the API — a masked suffix is returned, never the value. Keys are decrypted in the worker at run start and held in memory only, and are never written to run steps or logs.

### Dashboard

Deliberately minimal. This is an operations dashboard for one person checking on scheduled jobs, not a product surface — its job is to make state legible, and every element that does not serve that is omitted.

**Screens.** Five, no more:

| Screen | Contents |
|---|---|
| **Agents** (home) | The list: name, schedule in human form, enabled state, last run time, last run status. One row per agent. |
| **Agent** | Single-page form — prompts, model and base URL, tool grants, budgets, schedule, context mode. Create and edit are the same page. |
| **Runs** | Run history for an agent: time, status, duration, tokens. |
| **Run** | The trace: rendered prompt, then each step in order with tool name, arguments, and result, then the final output. Single scrollable page, live-updating while the run is active. |
| **Settings** | Provider keys, search provider key, Telegram bot token and chat id, failure-notification target. |

Memory browsing lives on the agent's page rather than as a sixth screen.

**Visual rules.** Dark only — a single theme, no light mode and no toggle. Helvetica, or the nearest system sans-serif. Flat solid colors; no shadows, no gradients, no animation, no transitions. Rounded buttons. Status is carried by a solid color and a word, never by a color alone. Layout is a single column of full-width rows, sized for a desktop browser.

**Explicitly not built:** charts, graphs, spend visualizations, an onboarding flow, a wizard, tooltips, modals beyond a delete confirmation, drag-and-drop, or any theming system. Empty states are one line of text.

The dashboard is a static bundle served by the API service, so `docker compose up` yields a working URL with no separate frontend container and no build step for the user.

### API contract — the REST/gRPC split

There are two protocol boundaries, and they are separate contracts chosen independently:

```
Browser ──REST/JSON──▶ API service ──gRPC──▶ Coordinator ──gRPC──▶ Worker
                            │
                            └──SQL──▶ Postgres
```

**PostgreSQL is the system of record, and every service reads it directly.** The coordinator is a participant in that database, not a gatekeeper in front of it. The API service therefore answers dashboard *queries* straight from Postgres — which is what the existing status handler already does — and calls the coordinator over gRPC only for *commands* that must reach a running worker. Routing reads through the coordinator would add a hop and a serialization round-trip to reach data the API service can already see.

The practical consequence is that the two contracts barely overlap:

| Operation | Path |
|---|---|
| Create, edit, list, delete agents and schedules | REST → Postgres |
| Read runs, run steps, output inbox | REST → Postgres |
| Browse, edit, delete memory records | REST → Postgres |
| Store and list secrets; read the tool catalog | REST → Postgres / in-process registry |
| Telegram chat-id discovery | REST → Telegram API |
| Live-tail a running run's steps | REST (server-sent events) → Postgres |
| **Trigger a run now** | REST → gRPC → coordinator |
| **Cancel a run in progress** | REST → gRPC → coordinator → worker |

Only the final two cross both boundaries and require a change in both contracts when they change. Everything else is defined once.

**Public edge (REST/JSON).** Resource-oriented collections and items for agents, schedules, runs, run steps, memory records, secrets, and the tool catalog, plus the command and discovery endpoints above. Live run output uses server-sent events, which requires nothing beyond the existing HTTP server — no WebSocket library, no second transport.

**Interior (protobuf/gRPC).** The service-to-service contract is extended: dispatch carries a run identifier rather than an opaque command string, and status reporting gains fields for output, token counts, cost, error, and lease renewal. The current status message carries only status and timestamps, and there is nowhere in the system today to put an agent's output.

**The third, implicit contract.** The dashboard's TypeScript types mirror the JSON the Go handlers emit, and nothing verifies that they agree — a renamed Go struct field surfaces as `undefined` in the browser rather than as a build failure. This is the boundary most likely to cause real debugging time, more so than the proto/JSON overlap above. Either generate TypeScript types from the Go structs, or maintain a single shared type definition treated as the contract; the choice is open, but one of them is required.

## Testing Decisions

### What makes a good test here

A good test drives Taskd the way the dashboard does — through the REST API — and asserts on what a user could observe: the run's status, its steps, its output, what arrived at Telegram, what's in memory. It does not reach into a service's internal state, does not assert on the number of times a function was called, and does not know how the executor is structured internally. A test should survive the executor being rewritten.

The existing suite has two tests that assert against the coordinator's worker pool map and a worker's received-task map. Those are implementation-detail assertions and are the only reason those fields are exported. They are left alone rather than rewritten, but they are not the pattern to copy.

### The seam

**One seam: the REST API, against a real in-process cluster and a real PostgreSQL.** Tests create an agent, trigger or schedule a run, and assert through the API.

This is achievable as a single seam because the product's own configurability is already the seam. Three things that would normally require mock interfaces are plain configuration, and tests point them at local HTTP test servers instead:

- **The model provider** — a custom base URL is a product feature for Ollama support, so a test server returns scripted tool-call sequences. No model-client interface is introduced.
- **Telegram** — the same base-URL override; the test server records what a run sent.
- **Fetch and search targets** — the agent fetches whatever URL the test gives it.

The executor, the loop, budget enforcement, memory writes, tool dispatch, and delivery are therefore all exercised end-to-end without a single mock object.

**One new abstraction is unavoidable: the clock.** The coordinator's materializer and reaper take an injectable time source, defaulting to real time. Timezone correctness, DST transitions, missed-run detection, and lease expiry cannot be tested in wall-clock time.

### Prior art

The existing integration suite already provides the harness: it boots a real coordinator, scheduler, and N workers in-process against a PostgreSQL container, waits for worker registration, and drives the system over HTTP and gRPC with a polling condition helper. It is extended rather than replaced — the cluster launcher gains the dashboard-facing API service, the fake model server, and the injectable clock. The end-to-end test that posts a task and polls for status transitions is the template every new test follows.

### What gets tested

- Creating an agent through the API and triggering a run produces a completed run with output.
- A scripted multi-step tool sequence produces the expected ordered run steps with arguments and results recorded.
- A run exceeding its step, token, or duration budget stops and is marked budget-exceeded rather than crashing or continuing.
- An agent granted memory tools writes records that a subsequent run reads back; an agent without those tools cannot reach memory.
- Memory read limits and namespace caps are enforced.
- A schedule fires at the correct instant for a given cron expression and timezone.
- A schedule whose fire time falls in the hour skipped by the spring daylight-saving transition does not fire that day.
- A schedule whose fire time falls in the hour repeated by the autumn daylight-saving transition fires exactly once, not twice.
- Timezone lookup succeeds inside the built container image, not only on a developer machine.
- A model returning a malformed or invented tool call has the error fed back as a tool result and the step counted against the budget; a model that never recovers terminates as failed rather than looping.
- A fire time that passes while nothing is running is recorded as missed and not executed late.
- A run whose lease expires is marked failed by the reaper.
- A run's output is delivered to the fake Telegram endpoint, chunked when it exceeds the message limit, and an inbox record exists regardless of whether the agent chose to send.
- Repeated agent failure produces a platform failure notification.
- Secrets are never returned in full by any API response and never appear in run steps.
- URL-taking tools refuse private and metadata addresses.
- Cancelling a run in progress stops it and marks it cancelled.
- A worker that shuts down mid-run finishes its current run within the grace period.

## Out of Scope

Deferred to post-v1, in rough priority order:

- **MCP server support.** The registry is shaped for it, but no MCP client, server auth, or tool enumeration ships in v1. This is sequencing, not a stance — a community project cannot have one maintainer as the integration bottleneck.
- **Multiple users and admin roles.** Schema carries ownership; there is no signup, invitation, or role UI.
- **Automatic retries** and configurable retry policy.
- **Overlap handling** — concurrency limits per agent when a run is still in flight.
- **Mid-run recovery** — resuming a partially executed run after a worker dies. Runs are failed, not resumed.
- **OAuth integrations** — Google Calendar, Gmail, and similar. Self-hosting makes OAuth harder rather than easier, since every operator would need to register their own application.
- **Semantic memory search.** Memory is namespace-and-key, not vectors.
- **Cost dashboards and spend aggregation.** Per-run cost is recorded; there is no reporting over it.
- **Agent chaining** — one agent triggering another, or shared memory between agents.
- **Event triggers** — webhook-initiated runs. Taskd is schedule-driven in v1.
- **GraphQL.** Considered and dropped in favor of gRPC internally with hand-written REST for the dashboard.
- **Scale-to-zero.** Precluded by the push-based topology, knowingly.
- **Mobile layouts.** The dashboard targets a desktop browser. No responsive breakpoints, no small-screen layout, no testing at phone widths.
- **Cost estimation in money.** Token counts are recorded and displayed; the column for cost exists and stays empty until there is a maintained price table behind it.
- **Prompt-injection defenses.** Content fetched from the open web enters the same context as an agent holding message-sending and memory-writing tools, with no human in the loop to intercept an instruction embedded in that content. Egress rules do not address this, because an injected instruction uses tools that were legitimately granted. This is a known and accepted risk in v1, and it widens when MCP support lands. Recorded here so that it is a decision rather than an oversight.

## Further Notes

**No issue tracker is configured for this repository.** There is no project configuration file, no label vocabulary, and the GitHub CLI is not installed on this machine, so this PRD could not be published as an issue with the `ready-for-agent` label. It lives in the repository's docs instead. Installing and authenticating the GitHub CLI would let it be filed against the existing remote.

**The template contributes less than its structure suggests.** What is genuinely inherited: the `FOR UPDATE SKIP LOCKED` claim pattern, worker self-registration with heartbeats, graceful-shutdown scaffolding, and the in-process cluster test harness. What is net-new: all recurrence (every task today is a single instant, retired once dispatched), any path for a result to travel back, the executor itself, ownership, timezone-correct timestamps, the worker registry in the database, backpressure, and automatic migrations.

**The Kubernetes target has a standing cost.** No scale-to-zero means paying for idle workers continuously to serve agents that run a few times a day, and managed control planes on the major clouds cost more per month than the workload does. Kubernetes manifests are for the operator who wants them and for the project's own learning goals; compose is the path almost every user will take.

**Licensing and naming remain open.** AGPL-3.0 is recommended — it keeps the project open and preserves the option of offering hosted Taskd later without a cloud provider reselling it; MIT if adoption matters more than monetization. The Go module path still points at the upstream template's repository and needs renaming regardless.

**Web search needs a provider decision.** Every credible search API requires a key. Either Taskd ships with a default provider and asks the operator to supply a key, or search is unavailable until one is configured. This affects the first-run experience directly and is not yet settled.
