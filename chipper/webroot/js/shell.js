// Shared page shell for the v3ctor web UI. Load it in <head> (no defer) on every page:
// it applies the saved theme, accent and font before the page paints, then adds the top bar,
// the toast area and the section tabs once the DOM is ready.

const PREFS = { theme: "v3ctor.theme", accent: "v3ctor.accent", font: "v3ctor.font" };

function readPref(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writePref(key, value) {
  try {
    if (value) {
      localStorage.setItem(key, value);
    } else {
      localStorage.removeItem(key);
    }
  } catch {
    // Storage can be unavailable (private windows); the choice then lasts for this page only.
  }
}

function applyPrefs() {
  const root = document.documentElement;
  const set = (attr, value) => (value ? root.setAttribute(attr, value) : root.removeAttribute(attr));
  set("data-theme", readPref(PREFS.theme));
  set("data-accent", readPref(PREFS.accent));
  set("data-font", readPref(PREFS.font));
}

applyPrefs();

function icon(name, extraClass = "") {
  return `<svg class="icon ${extraClass}" aria-hidden="true"><use href="/assets/icons.svg#${name}"></use></svg>`;
}

// ---------- Top bar ----------

const NAV = [
  { href: "/index.html", label: "Home", icon: "home", match: (p) => p === "/" || p === "/index.html" },
  { href: "/setup.html", label: "Server", icon: "sliders", match: (p) => p === "/setup.html" },
  { href: "/sdkapp/", label: "Robots", icon: "robot", match: (p) => p.startsWith("/sdkapp") },
];

const THEMES = ["", "light", "dark"]; // "" follows the system
const THEME_ICON = { "": "monitor", light: "sun", dark: "moon" };
const THEME_LABEL = { "": "Theme: system", light: "Theme: light", dark: "Theme: dark" };

function renderTopbar() {
  if (document.querySelector(".topbar") || document.body.dataset.shell === "none") {
    return;
  }
  const path = location.pathname;
  const header = document.createElement("header");
  header.className = "topbar";
  header.innerHTML = `
    <a class="brand" href="/index.html" aria-label="v3ctor home">
      <span class="brand-mark">${icon("robot")}</span><span class="brand-name">v3ctor</span>
    </a>
    <nav class="topnav" aria-label="Main">
      ${NAV.map((n) => `<a href="${n.href}" ${n.match(path) ? 'aria-current="page"' : ""}>${icon(n.icon)}<span>${n.label}</span></a>`).join("")}
    </nav>
    <button type="button" class="theme-toggle" id="themeToggle"></button>`;
  document.body.prepend(header);
  updateThemeToggle();
  header.querySelector("#themeToggle").addEventListener("click", () => {
    const current = readPref(PREFS.theme) || "";
    const next = THEMES[(THEMES.indexOf(current) + 1) % THEMES.length];
    writePref(PREFS.theme, next);
    applyPrefs();
    updateThemeToggle();
    const select = document.getElementById("theme-choose");
    if (select) {
      select.value = next;
    }
  });
}

function updateThemeToggle() {
  const button = document.getElementById("themeToggle");
  if (!button) {
    return;
  }
  const theme = readPref(PREFS.theme) || "";
  button.innerHTML = icon(THEME_ICON[theme]);
  button.title = THEME_LABEL[theme];
  button.setAttribute("aria-label", THEME_LABEL[theme]);
}

// ---------- Toasts ----------

function toast(message, kind) {
  let area = document.querySelector(".toasts");
  if (!area) {
    area = document.createElement("div");
    area.className = "toasts";
    area.setAttribute("role", "status");
    area.setAttribute("aria-live", "polite");
    document.body.appendChild(area);
  }
  const text = String(message ?? "");
  const type = kind || guessKind(text);
  const el = document.createElement("div");
  el.className = `toast ${type}`;
  el.innerHTML = `${icon(type === "error" ? "alert" : type === "success" ? "check-circle" : "info")}
    <span class="toast-text"></span>
    <button type="button" class="toast-close" aria-label="Dismiss">${icon("x")}</button>`;
  el.querySelector(".toast-text").textContent = text;
  const close = () => el.remove();
  el.querySelector(".toast-close").addEventListener("click", close);
  area.appendChild(el);
  setTimeout(close, type === "error" ? 9000 : 5000);
}

function guessKind(text) {
  const t = text.toLowerCase();
  if (/(error|fail|unable|could not|couldn't|invalid|missing|not found)/.test(t)) {
    return "error";
  }
  if (/(success|saved|done|added|deleted|removed|updated|applied)/.test(t)) {
    return "success";
  }
  return "info";
}

// The pages report results with alert(); show them as toasts instead of blocking dialogs.
// confirm() and prompt() stay native because the code waits for their answer.
window.alert = (message) => toast(message);

// ---------- Section tabs ----------
// <nav class="tabs"><a class="tab" data-tab="log" href="#log" onclick="showLog(); return false;">…</a></nav>
// The URL hash opens a tab directly (index.html#log); without one the first tab opens.

function initTabs() {
  const tabs = [...document.querySelectorAll(".tabs .tab[data-tab]")];
  if (!tabs.length) {
    return;
  }
  tabs.forEach((tab) =>
    tab.addEventListener("click", () => {
      tabs.forEach((t) => t.classList.toggle("active", t === tab));
      history.replaceState(null, "", `#${tab.dataset.tab}`);
      const bar = tab.parentElement; // keep the active tab visible when the bar scrolls (phones)
      bar.scrollTo({ left: tab.offsetLeft - (bar.clientWidth - tab.offsetWidth) / 2, behavior: "smooth" });
    })
  );
  const wanted = location.hash.slice(1);
  (tabs.find((t) => t.dataset.tab === wanted) || tabs[0]).click();
}

document.addEventListener("DOMContentLoaded", () => {
  renderTopbar();
  initTabs();
});
