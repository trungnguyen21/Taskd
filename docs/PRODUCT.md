# Taskd — Product Description

**Taskd is a self-hosted, open-source scheduler for AI agents.** You define an agent in a dashboard — a model, a prompt, a set of tools, optionally its own memory — attach a cron schedule, and Taskd runs it on that interval, forever. Results land in your Telegram and in an in-app inbox.

It is not a chat app and not a workflow builder. It is **cron for agents**: the missing piece between "I can prompt a model" and "this happens every morning without me."

---

## Who it's for

**Self-hosters and technical productivity enthusiasts** — the Open WebUI / Home Assistant / homelab audience. People who already run containers, already have an API key or an Ollama box, and already have a Telegram bot lying around.

This is a deliberate narrowing. Earlier framing was "anyone, any use case," which in practice means OAuth to thirty SaaS apps, a no-code builder, and forgiving error UX for people who can't read a stack trace. Self-hosting settles it: the audience is people who can `docker compose up`, and every downstream decision gets easier.

## Why it exists

ChatGPT Tasks already runs a scheduled prompt and emails you the result. Taskd differs on four axes that matter to this audience:

- **Your model, your key** — Anthropic, OpenAI, Gemini, or any OpenAI-compatible endpoint including Ollama, LM Studio, and vLLM.
- **Your data, your box** — nothing leaves your infrastructure except the model calls you configure.
- **Inspectable runs** — every run stores the rendered prompt, every tool call with arguments and results, token counts, and cost. An unattended agent is only trustworthy if you can answer "why did it do that" three weeks later.
- **Durable memory** — agents write records that later runs read back, so a scheduled job can reason about *change* rather than re-observing the world from scratch each time.

---

## Core concepts

### Agent

The unit users create at runtime from the dashboard:

| Field | Notes |
|---|---|
| `name` | |
| `model` | Provider + model id + base URL (enables Ollama and compatibles) |
| `system_prompt` / `user_prompt` | The instruction |
| `tools[]` | Selected from the tool catalog — capabilities are **grants**, not settings |
| `memory` | Implicit: an agent has memory iff `memory_read`/`memory_write` are granted |
| `context` | `fresh` (default) or `last_n(N)` — whether this run sees prior runs' outputs |
| `budgets` | max steps / max tokens / max duration, defaulted, overridable, hard-capped |
| `schedule` | Cron expression + IANA timezone |

### Run

One execution of an agent: a **tool-calling loop**. The model chooses tools, sees results, iterates until it answers or hits a budget. Every step is persisted.

Tool selection and a single model call are mutually exclusive — if the user picks tools, something must decide which to call and with what arguments, and that something is the model. The loop is therefore not optional, and its costs (budgets, long-lived workers, egress control, trace storage) are the real engineering of this product.

### Memory

`(namespace, key, content jsonb, created_at)`, append-only. Namespace defaults to the agent's own id, so behavior is per-agent-private today; cross-agent reads later become a grant row rather than a schema migration.

Reads require an explicit `limit`, and namespaces have hard record/byte caps. Unbounded memory silently inflates every run's token bill until a $0.02 agent costs $0.40.

### Tool catalog

**v1 is a closed first-party registry.** Five built-ins, all *capability-shaped* rather than service-shaped — none is "Notion" or "Slack," so none rots when a vendor changes an API, and none needs OAuth:

| Tool | Purpose |
|---|---|
| `web_search` | Research |
| `http_fetch` | Read any URL |
| `memory_read` / `memory_write` | Durable state |
| `send_telegram` | Reach the human |
| `send_webhook` | Universal escape hatch — Discord, ntfy, Home Assistant, anything |

The registry is **MCP-shaped internally**: a tool is `{ name, description, JSON Schema input, handler → content blocks }`, registered into a catalog the builder enumerates. Adding MCP servers post-v1 is then an adapter, not an executor rewrite — and for an open-source project, MCP is how contributors extend Taskd without you becoming the integration bottleneck.

### Delivery

Telegram is the primary channel: BotFather gives the operator a token in seconds, it pushes to their phone, and it needs no OAuth app, domain, or SMTP. The dashboard handles `chat_id` discovery, output is chunked against the 4096-character limit, and plain text is the default parse mode (MarkdownV2 escaping is a reliable source of 400s).

Every run's output is also written to an **in-app inbox** regardless of what the agent does — so Taskd is useful before a bot is configured, and so a confused agent that forgets to notify still leaves a record.

**Delivery is a tool, not a schedule property.** The model decides whether to send, which makes "only tell me if something actually changed" expressible.

**Failures notify separately**, per-user, through the platform rather than the agent — an agent with an invalid API key cannot report its own failure. Consecutive-failure alerting is the difference between noticing in an hour and noticing in a month.

---

## Scheduling semantics

- **Cron + IANA timezone**, 5-minute minimum granularity. "7am" is a wall-clock intent; the zone is what lets you recompute the next UTC instant across DST.
- **Missed runs are skipped and recorded as `missed`.** A 7am briefing delivered at 9am is worse than none, and catch-up storms after an outage burn a day of credits in a minute.
- **Overlap** — out of scope for v1; schedules are expected hours apart.
- **Lease + reaper** — runs carry `lease_expires_at`; the coordinator's existing scan tick marks expired runs `failed`. On Kubernetes this is required, not optional: rolling deploys, node scale-down, and evictions kill workers mid-run routinely.
- **No automatic retry.** An agent holding `send_telegram` and `memory_write` is not idempotent — a retry can double-send. Retry is opt-in per agent; manual re-run is always available from the dashboard.

## Security

- BYO provider keys, **envelope-encrypted at rest** with an app-level master key from the environment. Swapping in a KMS later touches one function.
- Keys are **write-only** in the UI — displayed as `sk-ant-…4f2a`, never rendered back.
- `http_fetch` and MCP servers make the executor an outbound HTTP engine; **egress rules block internal ranges and cloud metadata endpoints** (SSRF).
- Third-party tool descriptions are a prompt-injection surface aimed at an unattended agent holding the user's credentials. Relevant once MCP lands.
- Single-user login in v1, but **`user_id` is carried on every table from day one**. Retrofitting an owner column across agents, runs, schedules, and memory is a migration across the entire schema.

---

## Architecture

Inherited from the TaskMaster template and extended. Three services over gRPC, plus Postgres:

- **Scheduler** → becomes the **API service**: hand-written REST/JSON for the dashboard, gRPC client to the coordinator. GraphQL was considered and dropped — one client with fixed views doesn't repay a schema, resolvers, and codegen.
- **Coordinator** → materializes `schedules` into `runs` rows, dispatches to workers, reaps expired leases.
- **Worker** → executes the agent loop: resolve tools, call the model, run tool handlers, persist steps, deliver.
- **Postgres** → agents, schedules, runs, steps, memory, secrets, workers.

### What the template already gives you

`FOR UPDATE SKIP LOCKED` claim semantics (`coordinator.go:287`) — the correct Postgres queue pattern, reusable verbatim for runs. Worker self-registration with a self-reported address, heartbeats, and a graceful-shutdown skeleton.

### What is net-new

- **Recurrence.** There is none today — `tasks.scheduled_at` is a single instant and `picked_at` retires it forever. The entire premise of the product is unbuilt.
- **A result path.** Neither `api.proto` nor the `tasks` table has anywhere to put an agent's output. `UpdateTaskStatus` carries status and timestamps only.
- **`TIMESTAMPTZ`.** `setup.sql` uses `TIMESTAMP` (no zone) compared against `NOW()` — breaks on the first DST shift.
- **An owner column.** `tasks` has no user.
- **The executor** — `processTask` is `time.Sleep(5 * time.Second)`. Nothing has ever executed.
- **Worker registry in Postgres.** It's in-memory in the coordinator today, so replicas diverge and restarts forget the fleet — which pins you to `replicas: 1` on a platform that rolls pods constantly.
- **Backpressure.** The coordinator pushes on schedule regardless of worker capacity. Invisible with 5-second tasks; with 10-minute agent runs a full queue silently loses a run whose `picked_at` is already set.
- **Automatic migrations on boot.** `setup.sql` is applied by hand today; a self-hoster upgrading v1→v2 has no path.

### Deployment

**`docker compose up` is the primary, documented path** — the artifact that decides whether anyone tries this. Kubernetes manifests ship alongside for scale-out, with prebuilt images on GHCR so nobody has to build from source to evaluate it.

The push-based topology is a deliberate choice for its distributed-systems value, and it has a price: no scale-to-zero, per-pod addressing (workable on a plain Deployment via self-reported pod IPs), and a managed control plane that costs ~$72/mo on EKS/GKE Standard before any nodes — GKE Autopilot or DOKS are the realistic targets.

---

## v1 scope

**In:** runtime agent builder · 5 built-in tools · cron + timezone schedules · tool-calling loop with enforced budgets · per-agent memory · Telegram + inbox delivery · failure notification · full run trace · BYO keys (incl. Ollama) · single-user login · REST API + dashboard · docker compose + k8s manifests · Postgres worker registry · lease reaper · graceful shutdown.

**Out (backlog, in rough priority order):** GraphQL · MCP servers · multi-user + admin roles · automatic retries · overlap handling · mid-run recovery · OAuth integrations · semantic memory search · cost dashboards · agent chaining.

**License:** AGPL-3.0 recommended — keeps the project open and preserves a hosted-Taskd option later. MIT if adoption matters more than monetization.

---

## Open questions

- Name and Go module path (still `github.com/JyotinderSingh/task-queue`).
- Whether the closed tool registry stays a v1 sequencing decision — it should, since a community project can't have one maintainer as the integration bottleneck.
- Whether `web_search` ships with a default provider (Brave/Tavily/Exa all need a key) or requires the operator to bring one.
