// Boot and routing.
//
// Screens are addressed by fragment, so navigation never asks the server for a
// document and the whole dashboard is one file the browser already has.

import { api, ApiError, loadContract } from "./api.js";
import { el, replace, errorLine } from "./dom.js";
import { renderLogin } from "./screens/login.js";
import { renderAgents } from "./screens/agents.js";
import { renderAgent } from "./screens/agent.js";
import { renderRuns } from "./screens/runs.js";
import { renderRun } from "./screens/run.js";
import { renderSettings } from "./screens/settings.js";

const screen = document.getElementById("screen");
const chrome = document.getElementById("chrome");

const routes = [
  [/^\/agents$/, renderAgents],
  [/^\/agents\/new$/, (view) => renderAgent(view, null)],
  [/^\/agents\/([^/]+)$/, renderAgent],
  [/^\/agents\/([^/]+)\/runs$/, renderRuns],
  [/^\/runs\/([^/]+)$/, renderRun],
  [/^\/settings$/, renderSettings],
];

// Each navigation invalidates the one before it, so a screen that was waiting
// on the network - or polling a running run - cannot draw over its successor.
let generation = 0;

/** navigate changes screen. Screens call it instead of touching location. */
export function navigate(path) {
  window.location.hash = `#${path}`;
}

/** reload re-renders the current screen, after a save or a delete. */
export function reload() {
  render();
}

async function render() {
  const token = ++generation;
  const view = {
    container: screen,
    /** live reports whether this render is still the one on screen. */
    get live() {
      return token === generation;
    },
  };

  const path = window.location.hash.replace(/^#/, "") || "/agents";

  for (const [pattern, handler] of routes) {
    const match = pattern.exec(path);
    if (!match) continue;

    replace(screen, el("p", { class: "notice", text: "Loading…" }));
    try {
      await handler(view, ...match.slice(1));
    } catch (error) {
      if (!view.live) return;
      if (error instanceof ApiError && error.status === 401) {
        showLogin();
        return;
      }
      replace(screen, errorLine(error.message));
    }
    return;
  }

  replace(screen, el("p", { class: "empty", text: "No such screen." }));
}

function showLogin() {
  chrome.hidden = true;
  renderLogin(screen, async () => {
    chrome.hidden = false;
    await render();
  });
}

document.getElementById("sign-out").addEventListener("click", async () => {
  await api.logout();
  showLogin();
});

window.addEventListener("hashchange", render);

async function start() {
  await loadContract();
  const session = await api.session();
  if (!session.signed_in) {
    showLogin();
    return;
  }
  chrome.hidden = false;
  await render();
}

start();
