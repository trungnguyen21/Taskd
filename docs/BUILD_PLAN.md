# Build Plan — working checklist

Working document. The PRD is the source of truth and is not edited; this tracks
implementation order and progress against it.

## Slices

- [x] **1. Foundation: migrations + agents CRUD**
      Embedded SQL migrations applied on boot, `users` and `agents` tables,
      REST CRUD for agents, per-server HTTP mux.
- [ ] **2. Schedules + materializer**
      `schedules` and `runs` tables, cron + IANA timezone, injectable clock,
      forward-only fire-time rule, DST behaviour, missed-run recording.
- [ ] **3. Run dispatch + lease + reaper**
      Coordinator dispatches runs (not command strings), worker leases,
      reaper fails expired runs, worker backpressure.
- [ ] **4. Executor**
      OpenAI-compatible tool-calling loop, `run_steps` persistence, budgets,
      malformed-tool-call tolerance, fake model server in tests.
- [ ] **5. Tool registry**
      MCP-shaped registry; memory_read/write, http_fetch, web_search,
      send_telegram, send_webhook; egress rules; `memory_records` table.
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
