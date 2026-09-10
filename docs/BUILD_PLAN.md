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
- [ ] **5. Tool registry**
      memory_read/write, web_search, send_telegram, send_webhook;
      `memory_records` table. The MCP-shaped registry, `http_fetch` and the
      egress rules landed early in slice 4, because the loop needed a real
      tool to call.
- [ ] **6. Secrets + auth**
      `secrets` table, envelope encryption, write-only key API, single-user login.
- [ ] **7. Delivery + inbox + failure notification**
      Run output inbox, Telegram chunking, consecutive-failure alerting.
- [ ] **8. Dashboard**
      Five screens, dark-only, Helvetica, flat colours, served by the API service.
- [ ] **9. Operations**
      Health endpoints, graceful shutdown, embedded tzdata, k8s manifests.

## Notes

- Legacy `tasks` table and the `/schedule` endpoint stay until slice 3 retires them,
  so the inherited integration tests keep passing meanwhile.

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
