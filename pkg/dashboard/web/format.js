// Turning stored values into the words on the screen.

const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

/** timestamp renders an instant in the reader's own timezone. */
export function timestamp(value) {
  if (!value) return "—";
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return "—";
  return at.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** relative answers "how long ago", which is what the list is actually asked. */
export function relative(value) {
  if (!value) return "never";
  const then = new Date(value).getTime();
  if (Number.isNaN(then)) return "never";

  const seconds = Math.round((Date.now() - then) / 1000);
  const ago = seconds >= 0;
  const magnitude = Math.abs(seconds);

  const scale = [
    [60, "second"],
    [3600, "minute"],
    [86400, "hour"],
    [Infinity, "day"],
  ];
  const divisors = [1, 60, 3600, 86400];

  for (let i = 0; i < scale.length; i++) {
    if (magnitude < scale[i][0]) {
      const count = Math.floor(magnitude / divisors[i]);
      const unit = count === 1 ? scale[i][1] : `${scale[i][1]}s`;
      if (i === 0 && count < 10) return ago ? "just now" : "in a moment";
      return ago ? `${count} ${unit} ago` : `in ${count} ${unit}`;
    }
  }
  return timestamp(value);
}

/** duration renders how long a run took, from its own two timestamps. */
export function duration(startedAt, finishedAt) {
  if (!startedAt || !finishedAt) return "—";
  const seconds = Math.round(
    (new Date(finishedAt).getTime() - new Date(startedAt).getTime()) / 1000,
  );
  if (Number.isNaN(seconds) || seconds < 0) return "—";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${seconds % 60}s`;
}

/**
 * describeCron puts a schedule in human form for the cases a person actually
 * writes by hand, and returns the expression itself for the rest.
 *
 * Showing the raw expression is not a failure: it is what the user typed, and
 * it is better than a paraphrase this function guessed at. The agent form shows
 * real upcoming fire times from the server alongside it, which is the answer
 * that cannot be wrong.
 */
export function describeCron(expression, timezone) {
  const described = describeFields(expression);
  if (!described) return expression;
  return timezone ? `${described} (${timezone})` : described;
}

function describeFields(expression) {
  const fields = String(expression || "").trim().split(/\s+/);
  if (fields.length !== 5) return null;

  const [minute, hour, dayOfMonth, month, dayOfWeek] = fields;
  const everyDate = dayOfMonth === "*" && month === "*";
  const clock = at(hour, minute);

  if (everyDate && dayOfWeek === "*") {
    if (minute.startsWith("*/") && hour === "*") return `Every ${plural(minute.slice(2), "minute")}`;
    if (hour.startsWith("*/") && isNumber(minute)) {
      return `Every ${plural(hour.slice(2), "hour")} at ${pad(minute)} past`;
    }
    if (hour === "*" && isNumber(minute)) return `Hourly at ${pad(minute)} past`;
    if (clock) return `Daily at ${clock}`;
  }

  if (everyDate && clock) {
    const days = weekdays(dayOfWeek);
    if (days) return `${days} at ${clock}`;
  }

  if (isNumber(dayOfMonth) && month === "*" && dayOfWeek === "*" && clock) {
    return `Monthly on day ${dayOfMonth} at ${clock}`;
  }
  return null;
}

function weekdays(field) {
  if (field === "*") return null;
  if (field === "1-5") return "Weekdays";
  if (field === "0,6" || field === "6,0") return "Weekends";

  const names = field.split(",").map((part) => (isNumber(part) ? DAYS[Number(part) % 7] : null));
  if (names.some((name) => !name)) return null;
  if (names.length === 1) return `Every ${names[0]}`;
  return `Every ${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

function at(hour, minute) {
  if (!isNumber(hour) || !isNumber(minute)) return null;
  return `${pad(hour)}:${pad(minute)}`;
}

function isNumber(value) {
  return /^\d+$/.test(value);
}

function pad(value) {
  return String(Number(value)).padStart(2, "0");
}

function plural(count, unit) {
  return Number(count) === 1 ? unit : `${count} ${unit}s`;
}

/**
 * statusTone maps a run status onto one of three colours.
 *
 * The colour never travels alone - every caller renders the status word beside
 * it - so this is emphasis, not information.
 */
export function statusTone(status) {
  switch (status) {
    case "succeeded":
      return "ok";
    case "failed":
    case "budget_exceeded":
      return "bad";
    case "running":
    case "pending":
      return "busy";
    default:
      return "";
  }
}

/** statusWord spells a stored status the way it should be read. */
export function statusWord(status) {
  if (!status) return "never run";
  return String(status).replace(/_/g, " ");
}

/** isActive reports whether a run is still going, and so worth re-reading. */
export function isActive(status) {
  return status === "pending" || status === "running";
}

export function tokens(run) {
  const total = (run.prompt_tokens || 0) + (run.completion_tokens || 0);
  return total ? total.toLocaleString() : "—";
}
