// Agent - one page, whether the agent is being created or edited.
//
// Not a wizard. Everything an agent is - its prompts, its model, what it may
// call, what it may spend and when it runs - is on this page at once, because
// the point of the product is that describing a job takes thirty seconds.

import { api } from "../api.js";
import { el, replace, field, empty, errorLine } from "../dom.js";
import { navigate, reload } from "../app.js";
import { timestamp } from "../format.js";

const CEILINGS = { max_steps: 100, max_tokens: 2000000, max_duration_seconds: 3600 };

const BLANK = {
  name: "",
  description: "",
  model: "",
  base_url: "",
  system_prompt: "",
  user_prompt: "",
  tools: [],
  max_steps: 20,
  max_tokens: 100000,
  max_duration_seconds: 600,
  secret_name: "default",
  context_mode: "fresh",
  context_runs: 3,
  enabled: true,
};

export async function renderAgent(view, agentID) {
  const creating = !agentID;

  const [agent, tools, schedule] = await Promise.all([
    creating ? Promise.resolve({ ...BLANK }) : api.getAgent(agentID),
    api.listTools().catch(() => []),
    creating ? Promise.resolve(null) : api.getSchedule(agentID).catch(() => null),
  ]);
  if (!view.live) return;

  const message = el("div");
  const inputs = {};

  const text = (key, attributes = {}) =>
    (inputs[key] = el("input", { type: "text", value: agent[key] ?? "", ...attributes }));
  // A textarea's initial content is its child text, not a value attribute.
  const area = (key, attributes = {}) =>
    (inputs[key] = el("textarea", attributes, agent[key] ?? ""));
  const number = (key) =>
    (inputs[key] = el("input", { type: "number", min: "1", max: String(CEILINGS[key]), value: String(agent[key]) }));

  const enabled = el("input", { type: "checkbox", checked: agent.enabled });

  // Context ----------------------------------------------------------------
  const contextRuns = el("input", {
    type: "number",
    min: "1",
    value: String(agent.context_runs || 3),
  });
  const contextRunsField = field(
    "Previous runs to include",
    contextRuns,
    "How many earlier outputs the model sees.",
  );
  const contextMode = el(
    "select",
    {
      onchange: () => {
        contextRunsField.hidden = contextMode.value !== "last_n";
      },
    },
    [
      el("option", { value: "fresh", text: "Fresh — each run starts with no history" }),
      el("option", { value: "last_n", text: "Last N — each run sees recent outputs" }),
    ],
  );
  contextMode.value = agent.context_mode || "fresh";
  contextRunsField.hidden = contextMode.value !== "last_n";

  // Schedule ---------------------------------------------------------------
  const cron = el("input", {
    type: "text",
    class: "mono",
    value: schedule ? schedule.cron_expression : "",
    placeholder: "0 7 * * *",
  });
  const timezone = timezonePicker(schedule ? schedule.timezone : null);
  const preview = el("div", { class: "hint" });

  const refreshPreview = async () => {
    if (!cron.value.trim()) {
      replace(preview, "No schedule — this agent runs only when triggered.");
      return;
    }
    try {
      const result = await api.previewSchedule(cron.value.trim(), timezone.value);
      replace(preview, `Next: ${result.fire_times.map(timestamp).join(" · ")}`);
    } catch (error) {
      replace(preview, el("span", { class: "error", text: error.message }));
    }
  };
  cron.addEventListener("change", refreshPreview);
  timezone.addEventListener("change", refreshPreview);
  refreshPreview();

  // Save -------------------------------------------------------------------
  const save = async (event) => {
    event.preventDefault();
    replace(message);

    const payload = {
      name: inputs.name.value.trim(),
      description: inputs.description.value.trim(),
      model: inputs.model.value.trim(),
      base_url: inputs.base_url.value.trim(),
      system_prompt: inputs.system_prompt.value,
      user_prompt: inputs.user_prompt.value,
      tools: grants.selected(),
      max_steps: Number(inputs.max_steps.value),
      max_tokens: Number(inputs.max_tokens.value),
      max_duration_seconds: Number(inputs.max_duration_seconds.value),
      secret_name: inputs.secret_name.value.trim() || "default",
      context_mode: contextMode.value,
      context_runs: Number(contextRuns.value),
      enabled: enabled.checked,
    };

    try {
      const saved = creating
        ? await api.createAgent(payload)
        : await api.updateAgent(agentID, payload);

      // The schedule is a second resource but not a second step: it is saved
      // with the agent, because a job with no time attached is not a job.
      const expression = cron.value.trim();
      if (expression) {
        await api.setSchedule(saved.id, {
          cron_expression: expression,
          timezone: timezone.value,
          enabled: true,
        });
      } else if (schedule) {
        await api.deleteSchedule(saved.id);
      }

      if (creating) navigate(`/agents/${saved.id}`);
      else reload();
    } catch (error) {
      replace(message, errorLine(error.message));
      window.scrollTo(0, document.body.scrollHeight);
    }
  };

  const grants = toolGrants(tools, agent.tools || []);

  const form = el("form", { onsubmit: save }, [
    el("h2", { text: "Identity" }),
    field("Name", text("name", { required: true, placeholder: "Morning brief" })),
    field("Description", text("description"), "For your own reference. The model never sees it."),

    el("h2", { text: "Model" }),
    el("div", { class: "pair" }, [
      field("Model", text("model", { required: true, placeholder: "gpt-4o-mini" })),
      field("Base URL", text("base_url", { placeholder: "https://api.openai.com/v1" })),
    ]),
    field(
      "Credential",
      text("secret_name"),
      "Which stored key this agent's model calls use. Keys are managed in Settings.",
    ),

    el("h2", { text: "Prompts" }),
    field("System prompt", area("system_prompt"), "Who the agent is and how it should behave."),
    field("User prompt", area("user_prompt"), "The job itself, run on every schedule."),

    el("h2", { text: "Tools" }),
    grants.node,

    el("h2", { text: "Schedule" }),
    el("div", { class: "pair" }, [
      field("Cron expression", cron, "Five fields. Leave empty for no schedule."),
      field("Timezone", timezone, "A zone, not an offset, so 7am stays 7am across the year."),
    ]),
    el("div", { class: "field" }, preview),

    el("h2", { text: "Budgets" }),
    el("div", { class: "pair" }, [
      field("Max steps", number("max_steps"), `Up to ${CEILINGS.max_steps}.`),
      field("Max tokens", number("max_tokens"), `Up to ${CEILINGS.max_tokens.toLocaleString()}.`),
      field("Max seconds", number("max_duration_seconds"), `Up to ${CEILINGS.max_duration_seconds}.`),
    ]),

    el("h2", { text: "Context" }),
    field("Context mode", contextMode),
    contextRunsField,

    el("div", { class: "field" }, [
      el("label", { class: "check" }, [
        enabled,
        el("span", { text: "Enabled — an agent that is off never runs, scheduled or not." }),
      ]),
    ]),

    message,
    el("div", { class: "actions" }, [
      el("button", { class: "button primary", type: "submit", text: creating ? "Create agent" : "Save" }),
      creating ? null : el("button", { class: "button", type: "button", text: "Run now", onclick: () => runNow(agentID, message) }),
      creating ? null : el("a", { class: "button", href: `#/agents/${agentID}/runs`, text: "Run history" }),
      el("span", { class: "spacer" }),
      creating ? null : deleteControl(agentID),
    ]),
  ]);

  replace(
    view.container,
    el("div", { class: "screen-head" }, [
      el("h1", { text: creating ? "New agent" : agent.name }),
    ]),
    el("div", { class: "crumb" }, el("a", { href: "#/agents", text: "← All agents" })),
    form,
    creating ? null : await memorySection(view, agentID),
  );
}

async function runNow(agentID, message) {
  try {
    const run = await api.triggerRun(agentID);
    navigate(`/runs/${run.id}`);
  } catch (error) {
    replace(message, errorLine(error.message));
  }
}

/**
 * deleteControl asks twice rather than opening a dialog.
 *
 * Deleting an agent takes its runs and its memory with it, so it should not be
 * one click - but a modal for it is a whole mechanism this dashboard otherwise
 * does not have.
 */
function deleteControl(agentID) {
  const holder = el("span");
  const confirmRow = () =>
    replace(holder, [
      el("span", { class: "empty", text: "Delete this agent, its runs and its memory? " }),
      el("button", {
        class: "button danger",
        type: "button",
        text: "Yes, delete",
        onclick: async () => {
          await api.deleteAgent(agentID);
          navigate("/agents");
        },
      }),
      el("button", { class: "button quiet", type: "button", text: "Cancel", onclick: initial }),
    ]);

  const initial = () =>
    replace(
      holder,
      el("button", { class: "button danger", type: "button", text: "Delete", onclick: confirmRow }),
    );

  initial();
  return holder;
}

/**
 * memorySection puts what an agent has remembered on the agent's own page.
 *
 * Memory is a property of an agent, not a place of its own, and reading it
 * where the prompt that writes it is visible is what makes it useful.
 */
async function memorySection(view, agentID) {
  const holder = el("div");

  const draw = async () => {
    const records = await api.listMemory(agentID).catch(() => []);
    if (!view.live) return;

    replace(
      holder,
      el("h2", { text: "Memory" }),
      records.length === 0
        ? empty("Nothing remembered yet.")
        : records.map((record) =>
            el("div", { class: "row" }, [
              el("div", { class: "row-head" }, [
                el("span", { class: "row-title mono", text: `${record.namespace} / ${record.key}` }),
                el("button", {
                  class: "button danger",
                  type: "button",
                  text: "Forget",
                  onclick: async () => {
                    await api.deleteMemory(record.id);
                    await draw();
                  },
                }),
              ]),
              el("pre", { text: JSON.stringify(record.content, null, 2) }),
              el("div", { class: "row-detail", text: `Written ${timestamp(record.created_at)}` }),
            ]),
          ),
    );
  };

  await draw();
  return holder;
}

/**
 * toolGrants lists what this installation can offer, plus anything the agent
 * was granted that the installation no longer registers.
 *
 * The catalogue only contains tools whose credentials are configured, so a
 * grant that survives a credential being removed would otherwise disappear
 * from the form while remaining stored on the agent.
 */
function toolGrants(catalogue, granted) {
  const available = new Map(catalogue.map((tool) => [tool.name, tool.description]));
  const names = [...available.keys()];
  for (const name of granted) if (!available.has(name)) names.push(name);

  const boxes = new Map();
  const checks = names.map((name) => {
    const box = el("input", { type: "checkbox", checked: granted.includes(name) });
    boxes.set(name, box);
    return el("label", { class: "check" }, [
      box,
      el("span", { class: "mono", text: name }),
      el("span", {
        class: "description",
        text: available.get(name) ?? "not available on this installation",
      }),
    ]);
  });

  return {
    node:
      names.length === 0
        ? empty("No tools are configured on this installation.")
        : el("div", { class: "checks" }, checks),
    selected: () => names.filter((name) => boxes.get(name).checked),
  };
}

function timezonePicker(current) {
  const chosen = current || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  const zones = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : null;
  if (!zones) return el("input", { type: "text", value: chosen });

  if (!zones.includes(chosen)) zones.unshift(chosen);
  const select = el(
    "select",
    {},
    zones.map((zone) => el("option", { value: zone, text: zone })),
  );
  select.value = chosen;
  return select;
}
