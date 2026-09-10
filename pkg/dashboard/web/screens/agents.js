// Agents - the home screen.
//
// It opens on the list because the first question of the day is whether the
// jobs are healthy, and every row answers it without being clicked: schedule,
// enabled state, when it last ran and how that ended.

import { api } from "../api.js";
import { el, replace, empty } from "../dom.js";
import { describeCron, relative, statusTone, statusWord } from "../format.js";

export async function renderAgents(view) {
  const agents = await api.listAgents();
  if (!view.live) return;

  const head = el("div", { class: "screen-head" }, [
    el("h1", { text: "Agents" }),
    el("a", { class: "button primary", href: "#/agents/new", text: "New agent" }),
  ]);

  if (agents.length === 0) {
    replace(view.container, head, empty("No agents yet."));
    return;
  }

  replace(view.container, head, agents.map(agentRow));
}

function agentRow(agent) {
  const lastRun = agent.last_run;
  const failing = lastRun && (lastRun.status === "failed" || lastRun.status === "budget_exceeded");

  return el(
    "a",
    {
      // A failing agent is marked on the row edge as well as in words, so that
      // a broken job cannot hide in a list of working ones.
      class: failing ? "row failing" : "row",
      href: `#/agents/${agent.id}/runs`,
    },
    [
      el("div", { class: "row-head" }, [
        el("span", { class: "row-title", text: agent.name }),
        el("span", {
          class: `status ${lastRun ? statusTone(lastRun.status) : ""}`,
          text: lastRun ? statusWord(lastRun.status) : "never run",
        }),
      ]),
      el("div", { class: "row-detail", text: scheduleLine(agent) }),
      el("div", {
        class: "row-detail",
        text: lastRun ? `Last run ${relative(lastRun.scheduled_for)}` : "No runs recorded",
      }),
    ],
  );
}

function scheduleLine(agent) {
  if (!agent.enabled) return "Disabled";
  if (!agent.schedule) return "No schedule — runs only when triggered";

  const described = describeCron(agent.schedule.cron_expression, agent.schedule.timezone);
  if (!agent.schedule.enabled) return `${described} — schedule paused`;
  return `${described} — next ${relative(agent.schedule.next_fire_at)}`;
}
