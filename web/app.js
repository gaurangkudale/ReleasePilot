const app = document.querySelector("#app");
const dialog = document.querySelector("#analyze-dialog");
const form = document.querySelector("#analyze-form");
const toast = document.querySelector("#toast");

let releases = [];
let currentRelease = null;

document.addEventListener("click", (event) => {
  const route = event.target.closest("[data-route]");
  if (route) {
    event.preventDefault();
    setActiveRoute(route.dataset.route);
  }
  if (event.target.closest("[data-analyze]")) dialog.showModal();
  if (event.target.closest("[data-close]")) dialog.close();
  const view = event.target.closest("[data-release-id]");
  if (view) loadRelease(view.dataset.releaseId);
  if (event.target.closest("[data-reanalyze]") && currentRelease) reanalyze(currentRelease);
  if (event.target.closest("[data-export]") && currentRelease) exportReport(currentRelease);
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = form.querySelector("[type=submit]");
  const error = document.querySelector("#form-error");
  button.disabled = true;
  button.textContent = "Analyzing...";
  error.textContent = "";

  try {
    const payload = Object.fromEntries(new FormData(form));
    const response = await fetch("/api/analyze", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!response.ok) throw new Error(await response.text());
    currentRelease = await response.json();
    dialog.close();
    renderCockpit(currentRelease);
    showToast("Release analysis completed");
  } catch (err) {
    error.textContent = err.message;
  } finally {
    button.disabled = false;
    button.textContent = "Run analysis";
  }
});

function setActiveRoute(route) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.route === route));
  if (route === "settings") renderSettings();
  else loadReleases();
}

async function loadReleases() {
  renderLoading();
  const response = await fetch("/api/releases");
  releases = await response.json();
  renderReleases(releases);
}

async function loadRelease(id) {
  renderLoading();
  const response = await fetch(`/api/releases/${encodeURIComponent(id)}`);
  currentRelease = await response.json();
  renderCockpit(currentRelease);
}

async function reanalyze(release) {
  const response = await fetch("/api/analyze", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      repository: release.repository,
      baseBranch: release.baseBranch,
      releaseBranch: release.releaseBranch,
    }),
  });
  currentRelease = await response.json();
  renderCockpit(currentRelease);
  showToast("Release re-analyzed with current context");
}

function renderLoading() {
  app.innerHTML = `<div class="skeleton"></div><div class="skeleton"></div><div class="skeleton"></div>`;
}

function renderReleases(items) {
  app.innerHTML = `
    <header class="page-header">
      <div><p class="eyebrow">Release intelligence</p><h1>Releases</h1><p class="meta">Analyze release candidates before they reach production.</p></div>
      <button class="button primary" data-analyze>Analyze release</button>
    </header>
    <section class="release-list">
      ${items.map((item) => `
        <article class="release-row">
          <div><strong>${escapeHTML(item.name)}</strong><p>${escapeHTML(item.repository)} · ${escapeHTML(item.releaseBranch)}</p></div>
          <div><span class="label">Decision</span><strong class="${decisionClass(item.decision)}">${escapeHTML(item.decision)}</strong></div>
          <div><span class="label">Release health</span><strong>${item.health}/100</strong></div>
          <button class="button secondary" data-release-id="${escapeHTML(item.id)}">View report</button>
        </article>
      `).join("")}
    </section>`;
}

function renderCockpit(item) {
  const counts = countRisks(item.risks);
  app.innerHTML = `
    <header class="page-header">
      <div>
        <button class="back-link icon-button" data-route="releases">← All releases</button>
        <h1>${escapeHTML(item.name)} <span class="badge">${escapeHTML(item.target)} release</span></h1>
        <p class="meta">Release ID: ${escapeHTML(item.id)} · ${formatDate(item.createdAt)} · Initiated by ${escapeHTML(item.initiatedBy)}</p>
      </div>
      <div class="header-actions"><button class="button secondary" data-reanalyze>Re-analyze</button><button class="button primary" data-export>Export report</button></div>
    </header>
    <div class="cockpit">
      <section class="panel decision-panel">
        <div class="panel-header"><h3>Release decision</h3></div>
        <div class="decision-options">
          ${decisionOption("GO", "Recommended to proceed", "go", item.decision)}
          ${decisionOption("NEEDS VALIDATION", "Address risks or gaps", "validation", item.decision)}
          ${decisionOption("NO-GO", "Do not proceed", "no-go", item.decision)}
        </div>
        <p class="recommendation"><strong>Recommendation:</strong> ${escapeHTML(item.recommendation)}</p>
      </section>
      <section class="panel health-panel">
        <div class="panel-header"><h3>Release health</h3><span class="${item.health >= 70 ? "low" : "medium"}">${item.health >= 70 ? "Good" : "Attention needed"}</span></div>
        <div class="health-number">${item.health}<small>/100</small></div>
        <div class="health-track"><span style="width:${item.health}%"></span></div>
        <div class="risk-breakdown">
          <div><strong class="critical">${counts.Critical}</strong><span>Critical</span></div>
          <div><strong class="high">${counts.High}</strong><span>High</span></div>
          <div><strong class="medium">${counts.Medium}</strong><span>Medium</span></div>
          <div><strong class="low">${counts.Low}</strong><span>Low</span></div>
        </div>
      </section>
      <div class="data-grid">
        <section class="panel">
          <div class="panel-header"><h3>Validation checklist</h3><span class="meta">${item.validations.filter(v => v.status === "Passed").length}/${item.validations.length} passed</span></div>
          <table class="table"><thead><tr><th>Validation</th><th>Status</th><th>Evidence</th></tr></thead><tbody>
            ${item.validations.map((v) => `<tr><td><i class="status-dot ${escapeHTML(v.status)}"></i>${escapeHTML(v.name)}</td><td class="${v.status === "Passed" ? "low" : "medium"}">${escapeHTML(v.status)}</td><td class="evidence">${escapeHTML(v.evidence)}</td></tr>`).join("")}
          </tbody></table>
        </section>
        <section class="panel">
          <div class="panel-header"><h3>Risk findings</h3><span class="meta">${item.risks.length} detected</span></div>
          <table class="table"><thead><tr><th>Risk</th><th>Level</th><th>Impact</th><th>Evidence</th></tr></thead><tbody>
            ${item.risks.map((r) => `<tr><td>${escapeHTML(r.title)}</td><td class="${r.level.toLowerCase()}">${escapeHTML(r.level)}</td><td>${escapeHTML(r.impact)}</td><td class="evidence">${escapeHTML(r.evidence)}</td></tr>`).join("")}
          </tbody></table>
        </section>
        <section class="panel services-panel">
          <div class="panel-header"><h3>Affected services</h3><span class="meta">${item.services.length} services</span></div>
          <table class="table"><thead><tr><th>Service</th><th>Change</th><th>Blast radius</th></tr></thead><tbody>
            ${item.services.map((s) => `<tr><td>${escapeHTML(s.name)}</td><td>${escapeHTML(s.change)}</td><td class="${s.blastRadius.toLowerCase()}">${escapeHTML(s.blastRadius)}</td></tr>`).join("")}
          </tbody></table>
        </section>
      </div>
      <div class="bottom-grid">
        <section class="panel"><div class="panel-header"><h3>Rollback plan</h3></div><ol class="steps">${item.rollbackPlan.map((step) => `<li>${escapeHTML(step)}</li>`).join("")}</ol></section>
        <section class="panel"><div class="panel-header"><h3>Release notes</h3></div><ul class="notes">${item.releaseNotes.map((note) => `<li>${escapeHTML(note)}</li>`).join("")}</ul></section>
      </div>
    </div>`;
}

function renderSettings() {
  app.innerHTML = `<header class="page-header"><div><p class="eyebrow">Configuration</p><h1>Settings</h1></div></header>
    <section class="panel settings-panel"><h2>MVP settings</h2><p>Repository connections, AI provider configuration, and organization release policies will live here. They are intentionally excluded from the first working slice so the release decision workflow stays focused.</p></section>`;
}

function decisionOption(name, subtitle, className, selected) {
  return `<div class="decision-option ${className} ${name === selected ? "active" : ""}"><strong>${name}</strong><span>${subtitle}</span></div>`;
}

function countRisks(risks) {
  return risks.reduce((counts, risk) => ({ ...counts, [risk.level]: counts[risk.level] + 1 }), { Critical: 0, High: 0, Medium: 0, Low: 0 });
}

function decisionClass(decision) {
  if (decision === "GO") return "low";
  if (decision === "NO-GO") return "critical";
  return "medium";
}

function formatDate(value) {
  return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function exportReport(item) {
  const report = JSON.stringify(item, null, 2);
  const url = URL.createObjectURL(new Blob([report], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = `${item.id}-release-report.json`;
  link.click();
  URL.revokeObjectURL(url);
  showToast("Release report exported");
}

function showToast(message) {
  toast.textContent = message;
  toast.classList.add("show");
  window.setTimeout(() => toast.classList.remove("show"), 2200);
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[char]);
}

loadReleases();
