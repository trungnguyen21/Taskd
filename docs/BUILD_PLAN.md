# Build Plan — working checklist

Working document. The PRD is the source of truth and is not edited; this tracks
implementation order and progress against it.

## Slices

- [x] **1. Foundation: migrations + agents CRUD**
      Embedded SQL migrations applied on boot, `users` and `agents` tables,
      REST CRUD for agents, per-server HTTP mux.
- [x] **2. Schedules + materializer**
      `schedules` and `runs` tables, cron + IANA timezone, injectable clock,
      forward-only fire-time rule, DST behaviour, missed-run recording.
- [x] **3. Run dispatch + lease + reaper**
      Coordinator dispatches runs (not command strings), worker leases,
      reaper fails expired runs, worker backpressure.
- [x] **4. Executor**
      OpenAI-compatible tool-calling loop, `run_steps` persistence, budgets,
      malformed-tool-call tolerance, fake model server in tests.
- [x] **5. Tool registry**
      memory_read/write, web_search, send_telegram, send_webhook;
      `memory_records` table. The MCP-shaped registry, `http_fetch` and the
      egress rules landed early in slice 4, because the loop needed a real
      tool to call.
- [ ] **6. Secrets + auth**
      `secrets` table, envelope encryption, write-only key API, single-user login.
- [ ] **7. Delivery + inbox + failure notification**
      Run output inbox, Telegram chunking, consecutive-failure alerting.
- [x] **8. Dashboard**
      Five screens, dark-only, Helvetica, flat colours, served by the API service.
- [ ] **9. Operations**
      Health endpoints, graceful shutdown, embedded tzdata, k8s manifests.

## Notes

### Slice 8, decisions taken

**No frontend toolchain.** The PRD requires the bundle to be served by the API
service with no build step for the user. It is plain ES modules the browser
loads directly, embedded with `go:embed`, which makes that literally true rather
than "a build step someone else already ran". There is no npm, no package.json
and nothing to install.

**The implicit contract is made explicit.** The PRD names the browser/API
boundary as the one most likely to cost debugging time, and requires either
generated TypeScript or a single shared definition. `web/contract.json` is that
definition. `TestContractMatchesGoStructs` fails the build when a Go struct's
JSON tags drift from it, and `api.js` warns in the console when a response
carries fields it does not describe. A renamed field is meant to fail loudly in
both directions rather than surface as `undefined` in a screen.

**The agents list answers the home screen in one request.** `GET /api/agents`
returns each agent with its schedule and its last run. The screen exists to
answer "is anything broken", which the agent rows alone cannot answer, and the
N+1 this avoids is the same one that argued against GraphQL in the PRD.

**The run screen polls rather than streaming.** The API contract reserves
server-sent events for live-tailing a run, and that endpoint is not built yet.
Until it is, the run screen re-reads the two endpoints that do exist every two
seconds while a run is active and stops when it ends. Moving to SSE later
changes one function in `screens/run.js`.

**`TestWebSmoke` runs the JavaScript.** Node is a development convenience, not a
product dependency: the test skips when node is absent, and nothing about an
installation depends on it. It renders all five screens against payloads copied
from the real handlers, which is what catches a screen reading a field the API
does not send.

**Settings writes credentials the rest of the system does not read yet.** Tool
credentials still come from the operator's environment (`tools.ConfigFromEnv`).
The settings screen stores them under the names slice 7 will look for, so setup
finishes in one place, but until slice 7 reads from the secret store, a key
entered there does not register a tool. The screen lists what the installation
actually offers next to the form, so it does not claim otherwise.

**Not built here, because they are other slices.** Telegram chat-id discovery
(user story 44) and the unread marker on run output (49) are slice 7 - their
endpoints do not exist. Cancelling a run in progress needs the coordinator's
gRPC path and is not in this slice either.

- Legacy `tasks` table and the `/schedule` endpoint stay until slice 3 retires them,
  so the inherited integration tests keep passing meanwhile.

### Open question settled, slice 5

The PRD left it open whether `web_search` ships with a default provider or
requires the operator to bring a key. Settled: the tool is registered only when
a key is configured, and `GET /api/tools` returns what is actually registered.
A catalogue that lists a tool which fails the moment an agent calls it is worse
than one that is honest about what an installation can do. The same rule
applies to `send_telegram`, which needs a bot token and chat id.

Brave's API shape is what the search tool speaks. Any provider that matches it
works by pointing `TASKD_SEARCH_BASE_URL` elsewhere.

### Divergence from the PRD, slice 2

The PRD says the autumn daylight-saving duplicate is resolved by requiring each
computed fire time to be strictly later than the last one. Implementing it showed
that this is not sufficient, and the PRD is wrong on this point.

On the autumn transition the same wall-clock time occurs at two different
instants, and the second **is** strictly later than the first, so an
ordering rule accepts both and the agent runs twice. What separates them is the
wall-clock reading, not the ordering: the schedule stores the wall-clock form of
its last fire and skips a candidate the user would read identically.

`TestRepeatedAutumnHourWouldOtherwiseFireTwice` pins this - it asserts that the
cron library really does produce the duplicate, so the guard cannot be removed
as redundant. The forward-only rule is still applied; it is just not the part
doing the work here.
