// Settings - one page, so setup finishes in one place.
//
// Everything here is a stored credential. Values go in and never come back:
// the API returns a masked suffix, which is enough to tell "the key I set" from
// "some other key" and not enough to be worth stealing.

import { api } from "../api.js";
import { el, replace, field, empty, errorLine } from "../dom.js";
import { timestamp } from "../format.js";

// The names the rest of the system looks for. An operator should not have to
// guess them, so they are rows on the page rather than documentation.
const KNOWN = [
  {
    name: "default",
    label: "Model provider key",
    hint: "Used for an agent's model calls unless the agent names another credential.",
  },
  {
    name: "search_api_key",
    label: "Search provider key",
    hint: "Registers the web_search tool. Without it, the tool is not offered at all.",
  },
  {
    name: "telegram_bot_token",
    label: "Telegram bot token",
    hint: "From @BotFather. Registers send_telegram together with the chat id below.",
  },
  {
    name: "telegram_chat_id",
    label: "Telegram chat id",
    hint: "Message your bot once, then read the chat id from its getUpdates response.",
  },
  {
    name: "failure_notify_target",
    label: "Failure notification target",
    hint: "Where to report an agent that has failed repeatedly.",
  },
];

export async function renderSettings(view) {
  const [secrets, tools] = await Promise.all([
    api.listSecrets(),
    api.listTools().catch(() => []),
  ]);
  if (!view.live) return;

  const stored = new Map(secrets.map((secret) => [secret.name, secret]));
  const extra = secrets.filter((secret) => !KNOWN.some((known) => known.name === secret.name));

  replace(
    view.container,
    el("div", { class: "screen-head" }, el("h1", { text: "Settings" })),

    el("h2", { text: "Credentials" }),
    ...KNOWN.map((known) => secretRow(view, known, stored.get(known.name))),

    el("h2", { text: "Other stored keys" }),
    extra.length === 0
      ? empty("None. Add one to point an agent at a second provider.")
      : extra.map((secret) =>
          secretRow(view, { name: secret.name, label: secret.name, hint: "" }, secret),
        ),
    addRow(view),

    el("h2", { text: "Tools this installation offers" }),
    tools.length === 0
      ? empty("None configured.")
      : tools.map((tool) =>
          el("div", { class: "row" }, [
            el("div", { class: "row-head" }, el("span", { class: "row-title mono", text: tool.name })),
            el("div", { class: "row-detail", text: tool.description }),
          ]),
        ),
    el(
      "p",
      { class: "empty" },
      "A tool whose credential is missing is not listed, and cannot be granted to an agent.",
    ),
  );
}

function secretRow(view, known, secret) {
  const value = el("input", { type: "password", placeholder: secret ? "Replace…" : "Not set" });
  const message = el("div");

  const save = async () => {
    replace(message);
    if (!value.value.trim()) {
      replace(message, errorLine("A value is required."));
      return;
    }
    try {
      await api.putSecret(known.name, value.value.trim());
      value.value = "";
      await renderSettings(view);
    } catch (error) {
      replace(message, errorLine(error.message));
    }
  };

  const remove = async () => {
    try {
      await api.deleteSecret(known.name);
      await renderSettings(view);
    } catch (error) {
      replace(message, errorLine(error.message));
    }
  };

  return el("div", { class: "row" }, [
    el("div", { class: "row-head" }, [
      el("span", { class: "row-title", text: known.label }),
      el("span", {
        class: `status ${secret ? "ok" : ""}`,
        text: secret ? `set ${secret.masked_suffix}` : "not set",
      }),
    ]),
    known.hint ? el("div", { class: "row-detail", text: known.hint }) : null,
    el("div", { class: "row-detail mono", text: known.name }),
    el("div", { class: "pair", style: "margin-top:10px; align-items:flex-start" }, [
      value,
      el("div", { style: "flex:0 0 auto; display:flex; gap:8px" }, [
        el("button", { class: "button primary", type: "button", text: "Save", onclick: save }),
        secret
          ? el("button", { class: "button danger", type: "button", text: "Remove", onclick: remove })
          : null,
      ]),
    ]),
    message,
    secret ? el("div", { class: "row-detail", text: `Updated ${timestamp(secret.updated_at)}` }) : null,
  ]);
}

function addRow(view) {
  const name = el("input", { type: "text", class: "mono", placeholder: "anthropic" });
  const value = el("input", { type: "password", placeholder: "Key" });
  const message = el("div");

  const add = async () => {
    replace(message);
    if (!name.value.trim() || !value.value.trim()) {
      replace(message, errorLine("A name and a value are both required."));
      return;
    }
    try {
      await api.putSecret(name.value.trim(), value.value.trim());
      await renderSettings(view);
    } catch (error) {
      replace(message, errorLine(error.message));
    }
  };

  return el("div", { class: "row" }, [
    el("div", { class: "row-head" }, el("span", { class: "row-title", text: "Add a key" })),
    el("div", { class: "pair", style: "margin-top:10px" }, [
      field("Name", name, "What an agent's credential field refers to."),
      field("Value", value),
    ]),
    message,
    el("button", { class: "button primary", type: "button", text: "Add", onclick: add }),
  ]);
}
