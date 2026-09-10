// Run - the trace.
//
// One scrollable page, top to bottom: what the model was actually sent, then
// every step in the order it happened, then what came out. Nothing is behind a
// disclosure triangle, because an agent that ran unattended is only worth
// having if what it did can be read back without hunting for it.

import { api } from "../api.js";
import { el, replace, empty } from "../dom.js";
import { duration, isActive, statusTone, statusWord, timestamp, tokens } from "../format.js";

// While a run is going the page re-reads itself. The API contract reserves
// server-sent events for this; until that endpoint exists, polling gets the
// same result over the endpoints that are already there, and a run that
// finishes stops the polling rather than the user's laptop doing it.
const POLL_INTERVAL_MS = 2000;

export async function renderRun(view, runID) {
  let drawn = null;

  const draw = async () => {
    const [run, steps] = await Promise.all([api.getRun(runID), api.listSteps(runID)]);
    if (!view.live) return false;

    // Redrawing identical content would throw away the reader's scroll
    // position every two seconds while they were reading.
    const signature = `${run.status}:${steps.length}:${run.output.length}`;
    if (signature !== drawn) {
      drawn = signature;
      replace(view.container, page(run, steps));
    }
    return isActive(run.status);
  };

  let keepGoing = await draw();
  while (keepGoing && view.live) {
    await sleep(POLL_INTERVAL_MS);
    if (!view.live) return;
    keepGoing = await draw();
  }
}

function page(run, steps) {
  return [
    el("div", { class: "screen-head" }, [
      el("h1", { text: timestamp(run.scheduled_for) }),
      el("span", { class: `status ${statusTone(run.status)}`, text: statusWord(run.status) }),
    ]),
    el("div", { class: "crumb" }, [
      el("a", { href: `#/agents/${run.agent_id}/runs`, text: "← Run history" }),
    ]),
    el("div", {
      class: "row-detail",
      text: [
        run.trigger === "manual" ? "Triggered by hand" : "Scheduled",
        `started ${timestamp(run.started_at)}`,
        `took ${duration(run.started_at, run.finished_at)}`,
        `${tokens(run)} tokens`,
      ].join(" · "),
    }),

    run.error ? el("h2", { text: "Error" }) : null,
    run.error ? el("pre", { class: "error", text: run.error }) : null,

    el("h2", { text: "Prompt as sent" }),
    run.rendered_prompt
      ? el("pre", { text: run.rendered_prompt })
      : empty("The run has not reached the model yet."),

    el("h2", { text: "Steps" }),
    steps.length === 0 ? empty("No steps recorded yet.") : steps.map(stepRow),

    el("h2", { text: "Output" }),
    run.output ? el("pre", { text: run.output }) : empty(outputPlaceholder(run.status)),
  ];
}

function outputPlaceholder(status) {
  return isActive(status) ? "Still running." : "This run produced no output.";
}

function stepRow(step) {
  const label =
    step.kind === "tool_call" ? `${step.step_index + 1} · tool · ${step.tool_name}` : `${step.step_index + 1} · model`;

  return el("div", { class: "row" }, [
    el("div", { class: "step-label", text: label }),

    step.content ? part("said", step.content) : null,
    step.arguments ? part("arguments", pretty(step.arguments)) : null,
    step.result ? part("result", pretty(step.result)) : null,
    step.error ? el("div", { class: "step-part" }, el("pre", { class: "error", text: step.error })) : null,

    el("div", { class: "row-detail", text: timestamp(step.created_at) }),
  ]);
}

function part(label, body) {
  return el("div", { class: "step-part" }, [
    el("div", { class: "step-label", text: label }),
    el("pre", { text: body }),
  ]);
}

/** pretty indents tool JSON, and leaves anything else exactly as it was. */
function pretty(text) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
