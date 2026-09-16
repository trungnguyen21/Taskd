// The REST client. One place knows the URLs and the error shape.

/** ApiError carries the status so callers can tell "signed out" from "broken". */
export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

let contract = null;

/** loadContract reads the shared type definition both ends are pinned to. */
export async function loadContract() {
  if (!contract) {
    const response = await fetch("/contract.json");
    contract = await response.json();
  }
  return contract;
}

/**
 * check compares a decoded response against the shared type definition.
 *
 * A field renamed on the Go side would otherwise arrive as `undefined` in a
 * screen and be rendered as an empty cell - a wrong dashboard that looks like a
 * working one. This does not repair anything; it makes the drift say so.
 */
function check(typeName, payload) {
  if (!contract || !typeName) return payload;

  const expected = contract.types[typeName.replace("[]", "")];
  const sample = Array.isArray(payload) ? payload[0] : payload;
  if (!expected || !sample || typeof sample !== "object") return payload;

  const actual = Object.keys(sample);
  const missing = expected.filter((name) => !actual.includes(name));
  const extra = actual.filter((name) => !expected.includes(name));
  if (missing.length || extra.length) {
    console.warn(
      `contract drift on ${typeName}:`,
      missing.length ? `missing ${missing.join(", ")}` : "",
      extra.length ? `unexpected ${extra.join(", ")}` : "",
    );
  }
  return payload;
}

async function request(method, path, body, typeName) {
  const response = await fetch(path, {
    method,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (response.status === 204) return null;

  const text = await response.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      throw new ApiError(response.status, text);
    }
  }

  if (!response.ok) {
    throw new ApiError(response.status, (payload && payload.error) || response.statusText);
  }
  return check(typeName, payload);
}

export const api = {
  session: () => request("GET", "/api/session"),
  login: (password) => request("POST", "/api/login", { password }),
  logout: () => request("POST", "/api/logout", {}),

  listAgents: () => request("GET", "/api/agents", undefined, "AgentOverview[]"),
  getAgent: (id) => request("GET", `/api/agents/${id}`, undefined, "Agent"),
  createAgent: (agent) => request("POST", "/api/agents", agent, "Agent"),
  updateAgent: (id, agent) => request("PATCH", `/api/agents/${id}`, agent, "Agent"),
  deleteAgent: (id) => request("DELETE", `/api/agents/${id}`),

  getSchedule: (agentID) =>
    request("GET", `/api/agents/${agentID}/schedule`, undefined, "Schedule"),
  setSchedule: (agentID, schedule) =>
    request("PUT", `/api/agents/${agentID}/schedule`, schedule, "Schedule"),
  deleteSchedule: (agentID) => request("DELETE", `/api/agents/${agentID}/schedule`),
  previewSchedule: (cron_expression, timezone) =>
    request("POST", "/api/schedules/preview", { cron_expression, timezone }),

  listRuns: (agentID) => request("GET", `/api/agents/${agentID}/runs`, undefined, "Run[]"),
  triggerRun: (agentID) => request("POST", `/api/agents/${agentID}/runs`, {}, "Run"),
  getRun: (id) => request("GET", `/api/runs/${id}`, undefined, "Run"),
  listSteps: (runID) => request("GET", `/api/runs/${runID}/steps`, undefined, "Step[]"),

  listMemory: (agentID) =>
    request("GET", `/api/agents/${agentID}/memory`, undefined, "MemoryRecord[]"),
  deleteMemory: (id) => request("DELETE", `/api/memory/${id}`),

  listTools: () => request("GET", "/api/tools"),
  listSecrets: () => request("GET", "/api/secrets", undefined, "Secret[]"),
  putSecret: (name, value) => request("PUT", `/api/secrets/${name}`, { value }, "Secret"),
  deleteSecret: (name) => request("DELETE", `/api/secrets/${name}`),
};
