// Runs - one agent's history.
//
// Reached in one click from the list, because the moment a row shows a bad
// status the next thing wanted is what happened.

import { api } from "../api.js";
import { el, replace, empty, errorLine } from "../dom.js";
import { navigate } from "../app.js";
import { duration, statusTone, statusWord, timestamp, tokens } from "../format.js";

export async function renderRuns(view, agentID) {
  const [agent, runs] = await Promise.all([api.getAgent(agentID), api.listRuns(agentID)]);
  if (!view.live) return;

  const message = el("div");

  const head = el("div", { class: "screen-head" }, [
    el("h1", { text: agent.name }),
    el("button", {
      class: "button primary",
      type: "button",
      text: "Run now",
      onclick: async () => {
        try {
          const run = await api.triggerRun(agentID);
          navigate(`/runs/${run.id}`);
        } catch (error) {
          replace(message, errorLine(error.message));
        }
      },
    }),
    el("a", { class: "button", href: `#/agents/${agentID}`, text: "Edit agent" }),
  ]);

  replace(
    view.container,
    head,
    el("div", { class: "crumb" }, el("a", { href: "#/agents", text: "← All agents" })),
    message,
    runs.length === 0 ? empty("This agent has not run yet.") : runs.map(runRow),
  );
}

function runRow(run) {
  return el("a", { class: "row", href: `#/runs/${run.id}` }, [
    el("div", { class: "row-head" }, [
      el("span", { class: "row-title", text: timestamp(run.scheduled_for) }),
      el("span", { class: `status ${statusTone(run.status)}`, text: statusWord(run.status) }),
    ]),
    el("div", {
      class: "row-detail",
      text: [
        run.trigger === "manual" ? "Triggered by hand" : "Scheduled",
        `took ${duration(run.started_at, run.finished_at)}`,
        `${tokens(run)} tokens`,
      ].join(" · "),
    }),
    run.error ? el("div", { class: "error", text: run.error }) : null,
  ]);
}
