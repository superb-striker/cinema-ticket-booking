"use strict";

const methods = ["get", "post", "put", "patch", "delete", "options", "head"];
const operationsNode = document.getElementById("operations");
const searchNode = document.getElementById("operationSearch");
let operations = [];

function text(value) {
  const node = document.createElement("span");
  node.textContent = value;
  return node;
}

function render(filter = "") {
  operationsNode.replaceChildren();
  const needle = filter.trim().toLowerCase();
  const visible = operations.filter((item) => item.search.includes(needle));
  document.getElementById("operationCount").textContent =
    `${visible.length} of ${operations.length} operations`;
  const grouped = new Map();
  for (const item of visible) {
    if (!grouped.has(item.tag)) grouped.set(item.tag, []);
    grouped.get(item.tag).push(item);
  }
  for (const [tag, items] of grouped) {
    const section = document.createElement("section");
    section.className = "tag-section";
    const heading = document.createElement("h2");
    heading.textContent = tag;
    section.append(heading);
    for (const item of items) {
      const details = document.createElement("details");
      details.className = "operation";
      const summary = document.createElement("summary");
      const method = text(item.method.toUpperCase());
      method.className = `method ${item.method}`;
      const path = text(item.path);
      path.className = "path";
      summary.append(method, path, text(item.operation.summary || ""));
      const body = document.createElement("div");
      body.className = "operation-body";
      const metadata = document.createElement("div");
      metadata.className = "operation-meta";
      metadata.append(
        Object.assign(text(item.operation.operationId), { className: "pill" }),
      );
      metadata.append(
        Object.assign(
          text(item.operation.security ? "authenticated" : "public"),
          { className: "pill" },
        ),
      );
      const description = document.createElement("p");
      description.textContent =
        item.operation.description || item.operation.summary || "";
      const responseTitle = document.createElement("strong");
      responseTitle.textContent = "Responses";
      const responses = document.createElement("div");
      responses.className = "response-list";
      for (const [status, response] of Object.entries(
        item.operation.responses || {},
      )) {
        const badge = text(`${status} ${response.description || ""}`);
        badge.className = "pill";
        responses.append(badge);
      }
      body.append(metadata, description, responseTitle, responses);
      details.append(summary, body);
      section.append(details);
    }
    operationsNode.append(section);
  }
}

fetch("/openapi.yaml")
  .then((response) => {
    if (!response.ok)
      throw new Error(`OpenAPI request returned ${response.status}`);
    return response.json();
  })
  .then((spec) => {
    document.getElementById("apiTitle").textContent =
      `${spec.info.title} ${spec.info.version}`;
    document.getElementById("apiDescription").textContent =
      spec.info.description;
    for (const [path, item] of Object.entries(spec.paths)) {
      for (const method of methods) {
        if (!item[method]) continue;
        const operation = item[method];
        const tag = (operation.tags && operation.tags[0]) || "Other";
        operations.push({
          path,
          method,
          operation,
          tag,
          search:
            `${path} ${method} ${tag} ${operation.summary || ""}`.toLowerCase(),
        });
      }
    }
    operations.sort(
      (a, b) =>
        a.tag.localeCompare(b.tag) ||
        a.path.localeCompare(b.path) ||
        a.method.localeCompare(b.method),
    );
    render();
  })
  .catch((error) => {
    const node = document.getElementById("docsError");
    node.textContent = `Could not load API documentation: ${error.message}`;
    node.classList.remove("hidden");
  });

searchNode.addEventListener("input", () => render(searchNode.value));
