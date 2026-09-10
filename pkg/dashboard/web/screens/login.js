// The gate. Not one of the five screens - it exists only because an instance
// on a home network is otherwise open to everyone on it.

import { api, ApiError } from "../api.js";
import { el, replace, field, errorLine } from "../dom.js";

export function renderLogin(container, onSignedIn) {
  const password = el("input", { type: "password", id: "password", autofocus: true });
  const message = el("div");

  const form = el(
    "form",
    {
      onsubmit: async (event) => {
        event.preventDefault();
        replace(message);
        try {
          await api.login(password.value);
          await onSignedIn();
        } catch (error) {
          const text =
            error instanceof ApiError && error.status === 401
              ? "Incorrect password."
              : error.message;
          replace(message, errorLine(text));
          password.value = "";
          password.focus();
        }
      },
    },
    [
      el("h1", { text: "taskd" }),
      field("Password", password),
      message,
      el("button", { class: "button primary", type: "submit", text: "Sign in" }),
    ],
  );

  replace(container, el("div", { class: "login" }, form));
  password.focus();
}
