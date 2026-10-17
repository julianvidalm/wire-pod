// Appearance settings (index.html, "Appearance" tab). The saved values are applied on every page by
// js/shell.js; this file only wires the controls.

function showUICustomizer() {
  toggleVisibility(["section-log", "section-botauth", "section-intents", "section-version", "section-uicustomizer"], "section-uicustomizer", "icon-Customizer");
}

function setUITheme() {
  writePref(PREFS.theme, getValue("theme-choose"));
  applyPrefs();
  updateThemeToggle();
}

function setUIColor() {
  const accent = getValue("accent-color-choose");
  writePref(PREFS.accent, accent === "teal" ? "" : accent);
  applyPrefs();
}

function setUIFont() {
  const font = getValue("body-font-choose");
  writePref(PREFS.font, font === "system" ? "" : font);
  applyPrefs();
}

function getValue(element) {
  return document.getElementById(element).value;
}

function loadSettings() {
  const controls = {
    "theme-choose": readPref(PREFS.theme) || "",
    "accent-color-choose": readPref(PREFS.accent) || "teal",
    "body-font-choose": readPref(PREFS.font) || "system",
  };
  Object.entries(controls).forEach(([id, value]) => {
    const el = document.getElementById(id);
    if (el) {
      el.value = value;
    }
  });
}

loadSettings();
