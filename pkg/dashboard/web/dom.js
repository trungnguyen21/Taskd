// Element construction.
//
// Everything on this dashboard is built from these two functions rather than
// from HTML strings. That is not a style preference: the screens display model
// output, tool arguments and error text that no one on this end wrote, and a
// template that interpolates those into markup is an injection waiting for the
// first agent that fetches a page containing a script tag.

/**
 * el builds an element. Attributes go in the second argument; anything in the
 * third becomes a child, with strings appended as text rather than as markup.
 */
export function el(tag, attributes = {}, children = []) {
  const node = document.createElement(tag);

  for (const [name, value] of Object.entries(attributes)) {
    if (value === null || value === undefined || value === false) continue;

    if (name === "class") {
      node.className = value;
    } else if (name === "text") {
      node.textContent = value;
    } else if (name.startsWith("on") && typeof value === "function") {
      node.addEventListener(name.slice(2), value);
    } else if (name in node && name !== "list" && typeof value !== "string") {
      node[name] = value;
    } else {
      node.setAttribute(name, value);
    }
  }

  // Flattened all the way down: a screen composes lists inside sections, and a
  // child array that survived to here would reach the DOM as an array and
  // throw - or, worse, be quietly dropped.
  for (const child of [children].flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

/** replace empties a container and fills it with new children. */
export function replace(container, ...children) {
  container.replaceChildren(...children.flat(Infinity).filter(Boolean));
}

/** field wraps a labelled input, which is most of the agent and settings forms. */
export function field(label, control, hint) {
  return el("div", { class: "field" }, [
    el("label", { text: label, for: control.id || null }),
    control,
    hint ? el("div", { class: "hint", text: hint }) : null,
  ]);
}

/** Empty states are one line of text. */
export function empty(text) {
  return el("p", { class: "empty", text });
}

export function errorLine(text) {
  return el("p", { class: "error", text });
}
