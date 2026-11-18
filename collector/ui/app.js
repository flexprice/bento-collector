"use strict";

const state = { token: localStorage.getItem("collectorToken") || "", connections: [], collectors: [], draft: null, saved: null, preview: null, previewKey: "", tab: "source" };

// ---------- helpers ----------
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else if (k === "value") el.value = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

async function api(method, path, body) {
  const headers = { "Content-Type": "application/json" };
  if (state.token) headers.Authorization = "Bearer " + state.token;
  const res = await fetch(path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401) { renderAuth(true); throw new Error(data.error || "Unauthorized"); }
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function toast(msg, isErr) {
  const t = document.getElementById("toast");
  t.textContent = msg;
  t.className = "toast" + (isErr ? " err" : "");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => t.classList.add("hidden"), isErr ? 7000 : 3000);
}

function field(label, input, hint, wide) {
  return h("div", { class: wide ? "wide" : "" }, h("label", {}, label), input, hint ? h("div", { class: "hint" }, hint) : null);
}

function bindInput(obj, key, attrs = {}) {
  return h("input", { ...attrs, value: obj[key] ?? "", oninput: (e) => { obj[key] = e.target.value; onDraftChange(); } });
}

function select(options, value, onchange) {
  return h("select", { onchange: (e) => onchange(e.target.value) },
    options.map(([v, label]) => { const o = h("option", { value: v }, label); if (v === value) o.selected = true; return o; }));
}

const parseList = (s) => s.split(",").map((x) => x.trim()).filter(Boolean);
const parseKV = (s) => Object.fromEntries(s.split("\n").map((l) => l.split("=").map((x) => x.trim())).filter((p) => p.length >= 2 && p[0]).map((p) => [p[0], p.slice(1).join("=")]));
const formatKV = (o) => Object.entries(o || {}).map(([k, v]) => `${k} = ${v}`).join("\n");
const yesterday = () => new Date(Date.now() - 86400000).toISOString().slice(0, 10);

function canonical(c) {
  if (!c) return "";
  const { version, saved_at, saved_by, ...rest } = c;
  return JSON.stringify(rest, Object.keys(flatKeys(rest)).sort(), 2);
}
function flatKeys(o, acc = {}) { if (o && typeof o === "object") for (const k of Object.keys(o)) { acc[k] = 1; flatKeys(o[k], acc); } return acc; }

function lineDiff(a, b) {
  const x = a.split("\n"), y = b.split("\n");
  const m = x.length, n = y.length, dp = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = m - 1; i >= 0; i--) for (let j = n - 1; j >= 0; j--) dp[i][j] = x[i] === y[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out = []; let i = 0, j = 0;
  while (i < m && j < n) {
    if (x[i] === y[j]) { out.push([" ", x[i]]); i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) out.push(["-", x[i++]]);
    else out.push(["+", y[j++]]);
  }
  while (i < m) out.push(["-", x[i++]]);
  while (j < n) out.push(["+", y[j++]]);
  return out;
}

function diffView(before, after) {
  const lines = lineDiff(before, after).filter((l, idx, arr) => l[0] !== " " || arr.slice(Math.max(0, idx - 2), idx + 3).some((n) => n[0] !== " "));
  if (!lines.some((l) => l[0] !== " ")) return h("p", { class: "hint" }, "No changes from the saved version.");
  return h("pre", { class: "diff" }, lines.map(([k, t]) => h("span", { class: k === "+" ? "add" : k === "-" ? "del" : "" }, `${k} ${t}\n`)));
}

// ---------- auth ----------
function renderAuth(required) {
  const box = document.getElementById("auth");
  box.replaceChildren();
  if (!required && !state.token) return;
  const input = h("input", { type: "password", placeholder: "Collector token", value: state.token });
  box.append(h("div", { class: "row" }, input, h("button", { class: "btn outline", onclick: () => {
    state.token = input.value.trim(); localStorage.setItem("collectorToken", state.token); boot();
  } }, "Use token")));
}

// ---------- navigation ----------
async function loadLists() {
  [state.connections, state.collectors] = await Promise.all([api("GET", "/api/connections"), api("GET", "/api/collectors")]);
  const list = document.getElementById("collector-list");
  list.replaceChildren(...(state.collectors.length ? state.collectors.map((c) =>
    h("a", { href: `#/collectors/${c.id}`, class: "nav-link" + (location.hash === `#/collectors/${c.id}` ? " active" : "") },
      c.name || c.id, h("small", {}, `v${c.version} · ${c.mapping.event_name}`))) : [h("div", { class: "hint" }, "No collectors yet")]));
  document.getElementById("nav-connections").className = "nav-link" + (location.hash.startsWith("#/connections") ? " active" : "");
}

async function route() {
  try {
    await loadLists();
    const [, page, id] = location.hash.split("/");
    if (page === "collectors" && id === "new") return openCollector(null);
    if (page === "collectors" && id) return openCollector(id);
    if (page === "connections" || !state.connections.length) return renderConnections();
    if (state.collectors.length) { location.hash = `#/collectors/${state.collectors[0].id}`; return; }
    location.hash = "#/collectors/new";
  } catch (e) { toast(e.message, true); }
}

// ---------- connections ----------
function blankConnection(type) {
  return type === "flexprice"
    ? { id: "flexprice", type, url: "https://api.cloud.flexprice.io", auth_type: "api_key", secret: "", headers: {} }
    : { id: "", type: "prometheus", url: "", auth_type: "none", username: "", secret: "", headers: {} };
}

function renderConnections(editing) {
  const main = document.getElementById("main");
  const cards = state.connections.map((c) => {
    const result = h("span", { class: "hint" });
    return h("div", { class: "card" }, h("div", { class: "row" },
      h("div", {}, h("b", {}, c.id), " ", h("span", { class: "badge" }, c.type), " ",
        c.secret_ref ? h("span", { class: "badge " + (c.has_secret ? "ok" : "err") }, c.has_secret ? "secret set" : "secret missing") : h("span", { class: "badge muted" }, "no auth"),
        h("div", { class: "hint mono" }, c.url)),
      h("div", { class: "spacer" }), result,
      h("button", { class: "btn outline", onclick: async () => {
        result.textContent = "Testing…";
        try { const r = await api("POST", `/api/connections/${c.id}/test`); result.textContent = r.message; result.className = "badge " + (r.ok ? "ok" : "err"); }
        catch (e) { result.textContent = e.message; result.className = "badge err"; }
      } }, "Test connection"),
      h("button", { class: "btn outline", onclick: () => renderConnections({ ...c, secret: "" }) }, "Edit")));
  });
  main.replaceChildren(
    h("h1", {}, "Connections"),
    h("p", { class: "sub" }, "Where usage is read from (Prometheus) and where events are sent (Flexprice). Secrets are stored in the secret store, never in specs or git."),
    ...cards,
    h("div", { class: "row" },
      h("button", { class: "btn outline", onclick: () => renderConnections(blankConnection("prometheus")) }, "+ Prometheus connection"),
      h("button", { class: "btn outline", onclick: () => renderConnections(blankConnection("flexprice")) }, "+ Flexprice connection")),
    editing ? connectionForm(editing) : null);
}

function connectionForm(c) {
  const isNew = !state.connections.some((x) => x.id === c.id);
  const authOpts = c.type === "flexprice" ? [["api_key", "API key"]] : [["none", "None"], ["bearer", "Bearer token"], ["basic", "Basic auth"]];
  const body = h("div", { class: "grid" });
  const draw = () => {
    body.replaceChildren(
      field("ID", h("input", { value: c.id, disabled: !isNew || undefined, oninput: (e) => (c.id = e.target.value) }), "Lowercase, used by collectors to refer to this connection"),
      field("URL", h("input", { value: c.url, placeholder: c.type === "prometheus" ? "https://prometheus.internal:9090" : "https://api.cloud.flexprice.io", oninput: (e) => (c.url = e.target.value) })),
      field("Authentication", select(authOpts, c.auth_type || "none", (v) => { c.auth_type = v; draw(); })),
      c.auth_type === "basic" ? field("Username", h("input", { value: c.username || "", oninput: (e) => (c.username = e.target.value) })) : h("div"),
      c.auth_type && c.auth_type !== "none" ? field(c.type === "flexprice" ? "API key" : "Token / password",
        h("input", { type: "password", autocomplete: "new-password", placeholder: c.has_secret ? "•••••• (stored; leave empty to keep)" : "", oninput: (e) => (c.secret = e.target.value) }),
        "Written to the secret store; the UI never shows it again") : null,
      field("Extra headers", h("textarea", { oninput: (e) => (c.headers = parseKV(e.target.value)) }, formatKV(c.headers)),
        c.type === "prometheus" ? "One per line, e.g. X-Scope-OrgID = tenant-a" : "One per line, e.g. x-environment-id = <environment id> for self-hosted Flexprice", true));
  };
  draw();
  return h("div", { class: "card" }, h("h2", {}, isNew ? `New ${c.type} connection` : `Edit ${c.id}`), body,
    h("div", { class: "row" }, h("div", { class: "spacer" }),
      h("button", { class: "btn", onclick: async () => {
        try {
          const { has_secret, updated_at, ...payload } = c;
          if (!payload.secret) delete payload.secret;
          await api("PUT", `/api/connections/${encodeURIComponent(c.id)}`, payload);
          toast("Connection saved"); await loadLists(); renderConnections();
        } catch (e) { toast(e.message, true); }
      } }, "Save connection")));
}

// ---------- collectors ----------
function blankCollector() {
  const prom = state.connections.find((c) => c.type === "prometheus");
  const fp = state.connections.find((c) => c.type === "flexprice");
  return {
    id: "", name: "",
    source: { type: "prometheus", connection: prom ? prom.id : "", query: "", mode: "window" },
    trigger: { schedule: "15 0 * * *", window: "1d" },
    mapping: { event_name: "", customer: { from: "label", label: "", lookup: {} }, unit: "", required_labels: [], drop_labels: [], properties: {}, timestamp: "window_start" },
    destination: fp ? fp.id : "",
  };
}

async function openCollector(id) {
  state.saved = id ? await api("GET", `/api/collectors/${id}`) : null;
  state.draft = structuredClone(state.saved || blankCollector());
  state.preview = null; state.previewKey = ""; state.tab = "source";
  renderCollector();
}

function onDraftChange() {
  const stale = state.preview && state.previewKey !== canonical(state.draft);
  const flag = document.getElementById("preview-stale");
  if (flag) flag.classList.toggle("hidden", !stale);
}

function renderCollector() {
  const d = state.draft, isNew = !state.saved;
  const tabs = [["source", "1. Source"], ["mapping", "2. Mapping"], ["schedule", "3. Schedule & checks"], ["preview", "4. Preview"], ["save", "5. Save"], ["versions", "Versions"], ["config", "Rendered config"]];
  const panel = { source: sourceTab, mapping: mappingTab, schedule: scheduleTab, preview: previewTab, save: saveTab, versions: versionsTab, config: configTab }[state.tab]();
  document.getElementById("main").replaceChildren(
    h("div", { class: "row" }, h("div", {}, h("h1", {}, isNew ? "New collector" : d.name || d.id),
      h("p", { class: "sub" }, isNew ? "Query Prometheus once per UTC day and turn each result row into a Flexprice event."
        : `Version ${state.saved.version} saved ${new Date(state.saved.saved_at).toLocaleString()}`)),
      h("div", { class: "spacer" }), h("span", { id: "preview-stale", class: "badge err hidden" }, "Changed since last preview")),
    h("div", { class: "tabs" }, tabs.map(([k, l]) => h("button", { class: "tab" + (state.tab === k ? " active" : ""), onclick: () => { state.tab = k; renderCollector(); } }, l))),
    panel);
  onDraftChange();
}

function sourceTab() {
  const d = state.draft, isNew = !state.saved;
  const proms = state.connections.filter((c) => c.type === "prometheus");
  return h("div", { class: "card" }, h("div", { class: "grid" },
    field("Collector ID", bindInput(d, "id", { disabled: !isNew || undefined, placeholder: "syncron-cpu-hours" }), "Lowercase; also part of every event_id"),
    field("Display name", bindInput(d, "name", { placeholder: "Syncron CPU core-hours" })),
    field("Prometheus connection", select([["", "Select…"], ...proms.map((c) => [c.id, `${c.id} (${c.url})`])], d.source.connection, (v) => { d.source.connection = v; onDraftChange(); }),
      proms.length ? null : h("a", { href: "#/connections" }, "Add a Prometheus connection first")),
    field("Query mode", select([["window", "Daily window: evaluate at the end of the UTC day"], ["usage_date", "Snapshot: rows carry a usage_date label"]], d.source.mode, (v) => { d.source.mode = v; onDraftChange(); renderCollector(); }),
      d.source.mode === "usage_date" ? "Use __USAGE_DATE__ in the query; it is replaced with the day being collected." : "Use [1d] ranges; the query is evaluated at 00:00 UTC after the day."),
    field("PromQL", h("textarea", { class: "query", oninput: (e) => { d.source.query = e.target.value; onDraftChange(); } }, d.source.query),
      "Keep the labels you need with by (...). Everything else is summed away.", true)));
}

function mappingTab() {
  const d = state.draft, m = d.mapping;
  const fps = state.connections.filter((c) => c.type === "flexprice");
  const cust = h("div", { class: "grid wide" });
  const drawCust = () => cust.replaceChildren(
    field("Customer comes from", select([["label", "A Prometheus label"], ["static", "One fixed customer"]], m.customer.from, (v) => { m.customer.from = v; onDraftChange(); drawCust(); })),
    m.customer.from === "static"
      ? field("external_customer_id", bindInput(m.customer, "value", { placeholder: "cust_acme" }))
      : field("Label", bindInput(m.customer, "label", { placeholder: "label_tags_syncron_com_sy_tenant" }), "Must survive the query's by (...)"),
    m.customer.from === "label" ? field("Label value → external_customer_id",
      h("textarea", { placeholder: "trn_tQAAE7UDDQTNFS = cust_acme", oninput: (e) => { m.customer.lookup = parseKV(e.target.value); onDraftChange(); } }, formatKV(m.customer.lookup)),
      "Leave empty to use the label value itself as the customer ID. Rows whose value is not listed are rejected.", true) : null,
    m.customer.from === "label" ? field("Label values not billed", h("input", { value: (m.customer.skip || []).join(", "), placeholder: "trn_internal, trn_trial", oninput: (e) => { m.customer.skip = parseList(e.target.value); onDraftChange(); } }),
      "Internal or trial tenants. Their rows are counted as skipped, not rejected, so they do not fail the run.", true) : null);
  drawCust();
  return h("div", { class: "card" }, h("div", { class: "grid" },
    field("Event name", bindInput(m, "event_name", { placeholder: "syncron_cpu_hours" }), "Must match a Flexprice meter"),
    field("Unit", bindInput(m, "unit", { placeholder: "core_hours" }), "Stored as properties.unit"),
    cust,
    field("Required labels", h("input", { value: (m.required_labels || []).join(", "), oninput: (e) => { m.required_labels = parseList(e.target.value); onDraftChange(); } }), "Rows missing any of these are rejected, e.g. instance"),
    field("Labels to drop", h("input", { value: (m.drop_labels || []).join(", "), oninput: (e) => { m.drop_labels = parseList(e.target.value); onDraftChange(); } }), "All other labels become event properties"),
    field("Extra properties", h("textarea", { oninput: (e) => { m.properties = parseKV(e.target.value); onDraftChange(); } }, formatKV(m.properties)), "Fixed values added to every event, one per line: plan = enterprise"),
    field("Event timestamp", select([["window_start", "Start of the day (recommended)"], ["window_end", "End of the day"]], m.timestamp, (v) => { m.timestamp = v; onDraftChange(); }),
      "Start of day keeps the usage inside the billing period it belongs to"),
    field("Send to", select([["", "Select…"], ...fps.map((c) => [c.id, `${c.id} (${c.url})`])], d.destination, (v) => { d.destination = v; onDraftChange(); }))));
}

function scheduleTab() {
  const d = state.draft, t = d.trigger;
  const enabled = !!t.completeness;
  return h("div", { class: "card" }, h("div", { class: "grid" },
    field("Schedule (cron, UTC)", bindInput(t, "schedule"), "Used by the scheduler (CronJob / ECS). 15 0 * * * = 00:15 UTC daily"),
    field("Window", h("input", { value: "1 day (UTC)", disabled: true })),
    field("Completeness check", select([["off", "Off"], ["on", "Only send when the day is fully measured"]], enabled ? "on" : "off", (v) => {
      t.completeness = v === "on" ? { query: "count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440", min: 0.999 } : undefined; onDraftChange(); renderCollector();
    }), "If the check fails, the run sends nothing and exits with an error so the scheduler retries", true),
    enabled ? field("Completeness query", h("textarea", { oninput: (e) => { t.completeness.query = e.target.value; onDraftChange(); } }, t.completeness.query), "Evaluated at the end of the day", true) : null,
    enabled ? field("Minimum value", h("input", { type: "number", step: "0.001", value: t.completeness.min, oninput: (e) => { t.completeness.min = Number(e.target.value); onDraftChange(); } })) : null));
}

function previewTab() {
  const day = h("input", { type: "date", value: state.previewDay || yesterday(), max: yesterday(), onchange: (e) => (state.previewDay = e.target.value) });
  const out = h("div");
  const btn = h("button", { class: "btn", onclick: async () => {
    btn.disabled = true; btn.textContent = "Running…";
    try {
      state.preview = await api("POST", "/api/preview", { collector: state.draft, day: day.value });
      state.previewKey = canonical(state.draft);
      out.replaceChildren(previewResult(state.preview)); onDraftChange();
    } catch (e) { out.replaceChildren(h("ul", { class: "checks" }, h("li", { class: "lvl-error" }, e.message))); }
    btn.disabled = false; btn.textContent = "Run preview";
  } }, "Run preview");
  if (state.preview) out.append(previewResult(state.preview));
  return h("div", {}, h("div", { class: "card" }, h("div", { class: "row" }, h("label", {}, "Day"), day, btn,
    h("span", { class: "hint" }, "Runs the real query and mapping against live Prometheus. Nothing is sent to Flexprice."))), out);
}

function previewResult(r) {
  const events = r.rows.filter((x) => !x.error), rejects = r.rows.filter((x) => x.error);
  const comp = r.completeness ? (r.completeness.ok ? "complete" : "incomplete") : "not checked";
  return h("div", {},
    h("div", { class: "stats" },
      h("div", { class: "stat" }, h("b", {}, r.events), h("span", {}, "events ready")),
      h("div", { class: "stat" }, h("b", {}, r.rejected), h("span", {}, "rows rejected")),
      h("div", { class: "stat" }, h("b", {}, r.skipped), h("span", {}, "rows skipped")),
      h("div", { class: "stat" }, h("b", {}, comp), h("span", {}, "day completeness")),
      h("div", { class: "stat" }, h("b", {}, r.window.start.slice(0, 10)), h("span", {}, "UTC day"))),
    h("ul", { class: "checks" }, r.checks.map((c) => h("li", { class: "lvl-" + c.level }, c.message))),
    events.length ? h("div", { class: "card" }, h("h2", {}, "Events that would be sent"),
      h("table", {}, h("tr", {}, ["Customer", "Value", "Timestamp", "Properties", "event_id"].map((x) => h("th", {}, x))),
        events.slice(0, 200).map((x) => {
          const { value, unit, window_start, window_end, window_seconds, ...rest } = x.event.properties;
          return h("tr", {}, h("td", {}, x.event.external_customer_id), h("td", {}, `${value} ${unit || ""}`), h("td", { class: "mono" }, x.event.timestamp),
            h("td", { class: "mono" }, Object.entries(rest).map(([k, v]) => `${k}=${v}`).join("  ")), h("td", { class: "mono" }, x.event.event_id.slice(0, 16) + "…"));
        }))) : null,
    rejects.length ? h("div", { class: "card" }, h("h2", {}, "Rejected rows (not sent)"),
      h("table", {}, h("tr", {}, h("th", {}, "Reason"), h("th", {}, "Prometheus row")),
        rejects.slice(0, 200).map((x) => h("tr", {}, h("td", {}, x.error), h("td", { class: "mono" }, JSON.stringify(x.series)))))) : null,
    h("details", {}, h("summary", { class: "hint" }, "Query as sent"), h("pre", {}, `${r.query}\n\nevaluated at ${r.evaluated_at}`)));
}

function saveTab() {
  const d = state.draft;
  const fresh = state.preview && state.previewKey === canonical(d);
  const blocking = fresh && state.preview.checks.some((c) => c.level === "error");
  const confirm = h("input", { type: "checkbox", id: "force" });
  const btn = h("button", { class: "btn", disabled: !fresh || undefined, onclick: async () => {
    if (blocking && !confirm.checked) { toast("Preview has errors. Tick the box to save anyway.", true); return; }
    try {
      const saved = await api("PUT", `/api/collectors/${encodeURIComponent(d.id)}`, stripMeta(d));
      toast(`Saved ${saved.id} v${saved.version}`);
      location.hash = `#/collectors/${saved.id}`; await loadLists(); await openCollector(saved.id); state.tab = "versions"; renderCollector();
    } catch (e) { toast(e.message, true); }
  } }, state.saved ? `Save as v${state.saved.version + 1}` : "Save v1");
  return h("div", {},
    h("div", { class: "card" }, h("h2", {}, "Changes"), diffView(canonical(state.saved), canonical(d))),
    h("div", { class: "card" },
      !fresh ? h("ul", { class: "checks" }, h("li", { class: "lvl-warning" }, "Run a preview of this exact version before saving.")) : null,
      blocking ? h("ul", { class: "checks" }, h("li", { class: "lvl-error" }, "The last preview has errors. Saving now will make the next run fail."),
        h("li", {}, h("label", { class: "row" }, confirm, "I understand, save anyway"))) : null,
      fresh && !blocking ? h("ul", { class: "checks" }, h("li", { class: "lvl-ok" }, `Preview passed for ${state.preview.window.start.slice(0, 10)}: ${state.preview.events} events, ${state.preview.rejected} rejected.`)) : null,
      h("div", { class: "row" }, h("div", { class: "spacer" }), btn),
      h("p", { class: "hint" }, "Each save is a new immutable version. The next scheduled run picks up the latest version.")));
}

function stripMeta(c) { const { version, saved_at, saved_by, ...rest } = c; return rest; }

function versionsTab() {
  const out = h("div", {}, h("p", { class: "hint" }, "Loading…"));
  if (!state.saved) return h("div", { class: "card empty" }, "Save the collector to start its version history.");
  api("GET", `/api/collectors/${state.saved.id}/versions`).then((vs) => {
    out.replaceChildren(h("div", { class: "card" }, h("table", {}, h("tr", {}, ["Version", "Saved", "Event", "Changes vs previous", ""].map((x) => h("th", {}, x))),
      vs.map((v, i) => h("tr", {}, h("td", {}, `v${v.version}`, i === 0 ? h("span", { class: "badge ok" }, " live") : null), h("td", {}, new Date(v.saved_at).toLocaleString()),
        h("td", { class: "mono" }, v.mapping.event_name),
        h("td", {}, h("details", {}, h("summary", { class: "hint" }, "show diff"), diffView(canonical(vs[i + 1] || null), canonical(v)))),
        h("td", {}, i === 0 ? null : h("button", { class: "btn outline", onclick: () => {
          state.draft = structuredClone(v); state.draft.version = state.saved.version; state.preview = null; state.tab = "preview"; renderCollector();
          toast(`Loaded v${v.version} into the editor. Preview it, then save to roll back.`);
        } }, "Roll back to this")))))));
  }).catch((e) => out.replaceChildren(h("p", { class: "lvl-error" }, e.message)));
  return out;
}

function configTab() {
  const pre = h("pre", {}, "Rendering…");
  api("POST", "/api/render", { collector: state.draft, day: state.previewDay || yesterday() })
    .then((r) => (pre.textContent = r.yaml)).catch((e) => (pre.textContent = e.message));
  return h("div", { class: "card" }, h("p", { class: "hint" }, "Generated from the spec. Read-only: runs always render it fresh. Secrets appear as ${ENV} placeholders."), pre);
}

// ---------- boot ----------
async function boot() {
  renderAuth(false);
  await route();
}
document.getElementById("new-collector").addEventListener("click", () => { location.hash = "#/collectors/new"; });
window.addEventListener("hashchange", route);
boot();
