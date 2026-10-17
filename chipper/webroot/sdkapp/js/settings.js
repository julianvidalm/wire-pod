// Marks the tab that opens the given section as active (tabs: js/shell.js). Sections are "section-x",
// their tab icons "icon-x".
function updateColor(id) {
  const tab = document.getElementById(id.replace("section", "icon"))?.closest(".tab");
  if (!tab) {
    return;
  }
  tab.parentElement.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t === tab));
}
