const app = document.querySelector("#app");
const dialog = document.querySelector("#analyze-dialog");
const form = document.querySelector("#analyze-form");
const toast = document.querySelector("#toast");
const providerSelect = document.querySelector("#analysis-provider");
const repositorySelect = document.querySelector("#analysis-repository");

let releases = [];
let currentRelease = null;
let settings = null;

applyTheme(localStorage.getItem("releasepilot-theme") || "dark");

document.addEventListener("click", async (event) => {
  const route = event.target.closest("[data-route]");
  if (route) {
    event.preventDefault();
    setActiveRoute(route.dataset.route);
  }
  if (event.target.closest("[data-theme-toggle]")) toggleTheme();
  if (event.target.closest("[data-analyze]")) {
    dialog.showModal();
    await loadRepositoryOptions(providerSelect.value);
  }
  if (event.target.closest("[data-close]")) dialog.close();
  const deleteButton = event.target.closest("[data-delete-release-id]");
  if (deleteButton) {
    await deleteRelease(deleteButton.dataset.deleteReleaseId);
    return;
  }
  const view = event.target.closest("[data-release-id]");
  if (view) loadRelease(view.dataset.releaseId);
  if (event.target.closest("[data-reanalyze]") && currentRelease) reanalyze(currentRelease);
  if (event.target.closest("[data-export-pdf]") && currentRelease) exportPDF(currentRelease);
  const test = event.target.closest("[data-test-provider]");
  if (test) testConnection(test.dataset.testProvider, test);
  const loadModels = event.target.closest("[data-load-models]");
  if (loadModels) fetchModels(loadModels);
});

document.addEventListener("submit", async (event) => {
  if (event.target.id === "settings-form") {
    event.preventDefault();
    await saveSettings(event.target);
  }
});

providerSelect.addEventListener("change", () => loadRepositoryOptions(providerSelect.value));

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = form.querySelector("[type=submit]");
  const error = document.querySelector("#form-error");
  button.disabled = true;
  button.textContent = "Analyzing history...";
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
    showToast(currentRelease.aiGenerated ? "AI release report generated" : "Release analysis completed");
  } catch (err) {
    error.textContent = err.message;
  } finally {
    button.disabled = false;
    button.textContent = "Run analysis";
  }
});

function setActiveRoute(route) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.route === route));
  if (route === "settings") loadSettings();
  else loadReleases();
}

async function loadReleases() {
  renderLoading();
  const response = await fetch("/api/releases");
  releases = await response.json();
  renderReleases(releases);
}

async function loadSettings() {
  renderLoading();
  const response = await fetch("/api/settings");
  settings = await response.json();
  renderSettings(settings);
}

async function loadRepositoryOptions(provider) {
  repositorySelect.innerHTML = `<option value="">Loading repositories...</option>`;
  if (provider === "demo") {
    repositorySelect.innerHTML = `<option value="acme/payment-platform">acme/payment-platform</option>`;
    return;
  }
  try {
    const response = await fetch(`/api/repositories?provider=${encodeURIComponent(provider)}`);
    if (!response.ok) throw new Error(await response.text());
    const repositories = await response.json();
    repositorySelect.innerHTML = repositories.length
      ? repositories.map(repo => `<option value="${escapeHTML(repo.fullName)}" data-branch="${escapeHTML(repo.defaultBranch)}">${escapeHTML(repo.fullName)}${repo.private ? " · private" : ""}</option>`).join("")
      : `<option value="">No repositories found</option>`;
    const branch = repositorySelect.selectedOptions[0]?.dataset.branch;
    if (branch) form.elements.baseBranch.value = branch;
  } catch (error) {
    repositorySelect.innerHTML = `<option value="">Configure ${escapeHTML(provider)} in Settings</option>`;
  }
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
      provider: release.provider,
      repository: release.repository,
      baseBranch: release.baseBranch,
      releaseBranch: release.releaseBranch,
    }),
  });
  if (!response.ok) return showToast(await response.text());
  currentRelease = await response.json();
  renderCockpit(currentRelease);
  showToast("Release re-analyzed with current repository history");
}

async function deleteRelease(id) {
  const item = currentRelease?.id === id ? currentRelease : releases.find(release => release.id === id);
  const label = item?.name || id;
  const confirmed = window.confirm(`Delete "${label}" from local ReleasePilot reports? This only removes the saved local report.`);
  if (!confirmed) return;

  const response = await fetch(`/api/releases/${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!response.ok) {
    showToast(await response.text());
    return;
  }
  if (currentRelease?.id === id) currentRelease = null;
  showToast("Release deleted from local reports");
  await loadReleases();
}

async function saveSettings(settingsForm) {
  const button = settingsForm.querySelector("[type=submit]");
  button.disabled = true;
  button.textContent = "Saving...";
  const values = Object.fromEntries(new FormData(settingsForm));
  const payload = {
    github: { username: values.githubUsername, token: values.githubToken },
    gitlab: { username: values.gitlabUsername, token: values.gitlabToken },
    openai: { apiKey: values.openaiKey, model: values.openaiModel },
  };
  const response = await fetch("/api/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  button.disabled = false;
  button.textContent = "Save settings";
  if (!response.ok) return showToast(await response.text());
  settings = await response.json();
  renderSettings(settings);
  showToast("Settings saved securely in server memory");
}

async function testConnection(provider, button) {
  button.disabled = true;
  const original = button.textContent;
  button.textContent = "Testing...";
  const settingsForm = document.querySelector("#settings-form");
  const values = settingsForm ? Object.fromEntries(new FormData(settingsForm)) : {};
  const response = await fetch("/api/connections/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      provider,
      username: values[`${provider}Username`] || "",
      token: values[`${provider}Token`] || "",
      apiKey: values.openaiKey || "",
      model: values.openaiModel || "",
    }),
  });
  const result = response.ok ? await response.json() : { message: await response.text() };
  button.disabled = false;
  button.textContent = original;
  showToast(result.message);
}

async function fetchModels(button) {
  button.disabled = true;
  const original = button.textContent;
  button.textContent = "Loading models...";
  const response = await fetch("/api/openai/models");
  button.disabled = false;
  button.textContent = original;
  const modelList = document.querySelector("#model-list");
  if (!response.ok) {
    modelList.innerHTML = `<p class="form-error">${escapeHTML(await response.text())}</p>`;
    return;
  }
  const models = await response.json();
  modelList.innerHTML = models.length
    ? `<div class="model-list">${models.map(model => `<button type="button" class="model-chip" data-model-id="${escapeHTML(model.id)}">${escapeHTML(model.id)}</button>`).join("")}</div>`
    : `<p class="meta">No compatible models returned for this key.</p>`;
}

function renderLoading() {
  app.innerHTML = `<div class="skeleton"></div><div class="skeleton"></div><div class="skeleton"></div>`;
}

function renderReleases(items) {
  app.innerHTML = `
    <header class="page-header">
      <div><p class="eyebrow">Release intelligence</p><h1>Releases</h1><p class="meta">Analyze real GitHub and GitLab history before production.</p></div>
      <button class="button primary" data-analyze>Analyze release</button>
    </header>
    <section class="release-list">
      ${items.length ? items.map((item) => `
        <article class="release-row">
          <div><strong>${escapeHTML(item.name)}</strong><p>${escapeHTML(item.provider)} · ${escapeHTML(item.repository)} · ${escapeHTML(item.releaseBranch)}</p></div>
          <div><span class="label">Decision</span><strong class="${decisionClass(item.decision)}">${escapeHTML(item.decision)}</strong></div>
          <div><span class="label">Release health</span><strong>${item.health}/100</strong></div>
          <div class="row-actions">
            <button class="button secondary" data-release-id="${escapeHTML(item.id)}">View report</button>
            <button class="button danger" data-delete-release-id="${escapeHTML(item.id)}" aria-label="Delete ${escapeHTML(item.name)} locally">Delete</button>
          </div>
        </article>
      `).join("") : `<div class="empty-state"><strong>No local release reports yet.</strong><p>Run an analysis to create your first saved report.</p></div>`}
    </section>`;
}

function renderCockpit(item) {
  const agents = item.agents || [];
  const blastRadius = item.blastRadius || { nodes: [], edges: [] };
  const changedFiles = item.changedFiles || [];
  const commits = item.commits || [];
  const risks = item.risks || [];
  const validations = item.validations || [];
  const services = item.services || [];
  const counts = countRisks(risks);
  app.innerHTML = `
    <header class="page-header">
      <div>
        <button class="back-link icon-button" data-route="releases">← All releases</button>
        <h1>${escapeHTML(item.name)} <span class="badge">${item.aiGenerated ? "OpenAI report" : "Rule-based report"}</span></h1>
        <p class="meta">${escapeHTML(item.provider)} · ${escapeHTML(item.repository)} · ${commits.length} commits · ${changedFiles.length} changed files · ${formatDate(item.createdAt)}</p>
      </div>
      <div class="header-actions"><button class="button secondary" data-reanalyze>Re-analyze</button><button class="button danger" data-delete-release-id="${escapeHTML(item.id)}">Delete local</button><button class="button primary" data-export-pdf>Export PDF</button></div>
    </header>
    <section class="panel summary-panel"><div class="panel-header"><h3>Executive summary</h3></div><p>${escapeHTML(item.summary)}</p></section>
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
          <div><strong class="critical">${counts.Critical}</strong><span>Critical</span></div><div><strong class="high">${counts.High}</strong><span>High</span></div><div><strong class="medium">${counts.Medium}</strong><span>Medium</span></div><div><strong class="low">${counts.Low}</strong><span>Low</span></div>
        </div>
      </section>
      <section class="panel agents-panel">
        <div class="panel-header"><h3>Specialized AI risk agents</h3><span class="meta">${agents.length} agent passes</span></div>
        <div class="agent-grid">
          ${agents.map(agent => `
            <article class="agent-card ${agent.status.toLowerCase()}">
              <div><strong>${escapeHTML(agent.name)}</strong><span>${escapeHTML(agent.domain)}</span></div>
              <b>${escapeHTML(agent.status)}</b>
              <p>${escapeHTML(agent.summary)}</p>
              <small>${agent.confidence}% confidence · ${agent.findings.length} findings</small>
            </article>
          `).join("")}
        </div>
      </section>
      <section class="panel blast-panel">
        <div class="panel-header"><h3>Blast radius visualizer</h3><span class="meta">${(blastRadius.nodes || []).length} nodes</span></div>
        ${renderBlastRadius(blastRadius)}
      </section>
      <div class="data-grid">
        ${tablePanel("Validation checklist", `${validations.filter(v => v.status === "Passed").length}/${validations.length} passed`, ["Validation","Status","Evidence"], validations.map(v => [`<i class="status-dot ${escapeHTML(v.status)}"></i>${escapeHTML(v.name)}`, `<span class="${v.status === "Passed" ? "low" : "medium"}">${escapeHTML(v.status)}</span>`, `<span class="evidence">${escapeHTML(v.evidence)}</span>`]))}
        ${tablePanel("Risk findings", `${risks.length} detected`, ["Risk","Level","Impact","Evidence"], risks.map(r => [escapeHTML(r.title), `<span class="${String(r.level || "medium").toLowerCase()}">${escapeHTML(r.level)}</span>`, escapeHTML(r.impact), `<span class="evidence">${escapeHTML(r.evidence)}</span>`]))}
        ${tablePanel("Affected services", `${services.length} services`, ["Service","Change","Blast radius"], services.map(s => [escapeHTML(s.name), escapeHTML(s.change), `<span class="${String(s.blastRadius || "medium").toLowerCase()}">${escapeHTML(s.blastRadius)}</span>`]), "services-panel")}
        ${tablePanel("Changed files", `${changedFiles.length} files`, ["Path","Status","+/-"], changedFiles.slice(0, 8).map(f => [escapeHTML(f.path), escapeHTML(f.status), `+${f.additions} / -${f.deletions}`]), "files-panel")}
      </div>
      <div class="bottom-grid">
        <section class="panel"><div class="panel-header"><h3>Rollback plan</h3></div><ol class="steps">${item.rollbackPlan.map(step => `<li>${escapeHTML(step)}</li>`).join("")}</ol></section>
        <section class="panel"><div class="panel-header"><h3>Release notes</h3></div><ul class="notes">${item.releaseNotes.map(note => `<li>${escapeHTML(note)}</li>`).join("")}</ul></section>
      </div>
    </div>`;
}

function renderSettings(config) {
  app.innerHTML = `<header class="page-header"><div><p class="eyebrow">Configuration</p><h1>Settings</h1><p class="meta">Tokens are write-only, kept server-side, and never returned to this page.</p></div></header>
    <form id="settings-form" class="settings-grid">
      ${providerSettings("GitHub", "github", config.github, "Use a fine-grained personal access token with read-only repository Contents and Metadata permissions.")}
      ${providerSettings("GitLab", "gitlab", config.gitlab, "Use a personal access token with the read_api scope only.")}
      <section class="panel settings-card">
        <div class="panel-header"><h3>OpenAI</h3><span class="connection-status ${config.openai.configured ? "configured" : ""}">${config.openai.configured ? `Configured via ${escapeHTML(config.openai.source)}` : "Not configured"}</span></div>
        <p class="meta">OpenAI generates the release summary, risk report, rollback plan, and release notes.</p>
        <label>API key<input type="password" name="openaiKey" autocomplete="new-password" placeholder="${config.openai.configured ? "Configured · leave blank to keep" : "Enter API key"}"></label>
        <label>Model<input name="openaiModel" value="${escapeHTML(config.openai.model)}" required></label>
        <div class="inline-actions"><button type="button" class="button secondary" data-test-provider="openai">Check configuration</button><button type="button" class="button secondary" data-load-models>List models</button></div>
        <div id="model-list"></div>
      </section>
      <section class="panel settings-card">
        <div class="panel-header"><h3>Appearance</h3></div>
        <p class="meta">The selected theme is stored only in this browser.</p>
        <button type="button" class="button secondary" data-theme-toggle>Toggle light / dark theme</button>
      </section>
      <div class="settings-actions"><button class="button primary" type="submit">Save settings</button></div>
    </form>`;
}

function providerSettings(title, key, config, guidance) {
  return `<section class="panel settings-card">
    <div class="panel-header"><h3>${title}</h3><span class="connection-status ${config.configured ? "configured" : ""}">${config.configured ? "Token configured" : "Not configured"}</span></div>
    <p class="meta">${guidance}</p>
    <label>Username<input name="${key}Username" value="${escapeHTML(config.username)}" autocomplete="username"></label>
    <label>Read-only access token<input type="password" name="${key}Token" autocomplete="new-password" placeholder="${config.configured ? "Configured · leave blank to keep" : "Enter read-only token"}"></label>
    <button type="button" class="button secondary" data-test-provider="${key}">Test connection</button>
  </section>`;
}

function tablePanel(title, meta, headers, rows, className = "") {
  return `<section class="panel ${className}"><div class="panel-header"><h3>${title}</h3><span class="meta">${meta}</span></div><div class="table-wrap"><table class="table"><thead><tr>${headers.map(h => `<th>${h}</th>`).join("")}</tr></thead><tbody>${rows.map(row => `<tr>${row.map(cell => `<td>${cell}</td>`).join("")}</tr>`).join("")}</tbody></table></div></section>`;
}

function renderBlastRadius(graph) {
  if (!graph || !graph.nodes || graph.nodes.length === 0) {
    return `<p class="meta">No blast-radius graph available for this report.</p>`;
  }
  const release = graph.nodes.find(node => node.id === "release") || graph.nodes[0];
  const children = graph.nodes.filter(node => node.id !== release.id);
  return `<div class="blast-map">
    <div class="blast-center ${severityClass(release.severity)}">
      <strong>${escapeHTML(release.label)}</strong>
      <span>${escapeHTML(release.kind)}</span>
    </div>
    <div class="blast-nodes">
      ${children.map(node => `
        <div class="blast-node ${severityClass(node.severity)}" title="${escapeHTML(node.evidence)}">
          <strong>${escapeHTML(node.label)}</strong>
          <span>${escapeHTML(node.kind)} · ${escapeHTML(node.severity)}</span>
        </div>
      `).join("")}
    </div>
  </div>`;
}

function severityClass(value) {
  return String(value || "medium").toLowerCase();
}

document.addEventListener("click", (event) => {
  const model = event.target.closest("[data-model-id]");
  if (!model) return;
  const input = document.querySelector('input[name="openaiModel"]');
  if (input) input.value = model.dataset.modelId;
});

function decisionOption(name, subtitle, className, selected) {
  return `<div class="decision-option ${className} ${name === selected ? "active" : ""}"><strong>${name}</strong><span>${subtitle}</span></div>`;
}
function countRisks(risks) { return risks.reduce((counts, risk) => ({ ...counts, [risk.level]: counts[risk.level] + 1 }), { Critical: 0, High: 0, Medium: 0, Low: 0 }); }
function decisionClass(decision) { return decision === "GO" ? "low" : decision === "NO-GO" ? "critical" : "medium"; }
function formatDate(value) { return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
function exportPDF(item) { window.location.href = `/api/releases/${encodeURIComponent(item.id)}/pdf`; showToast("Preparing PDF report"); }
function toggleTheme() { applyTheme(document.documentElement.dataset.theme === "light" ? "dark" : "light"); }
function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem("releasepilot-theme", theme);
  document.querySelectorAll("[data-theme-toggle]").forEach(button => button.textContent = theme === "dark" ? "Light mode" : "Dark mode");
}
function showToast(message) { toast.textContent = message; toast.classList.add("show"); window.setTimeout(() => toast.classList.remove("show"), 2800); }
function escapeHTML(value) { return String(value ?? "").replace(/[&<>"']/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[char]); }

loadReleases();
