# ReleasePilot

ReleasePilot is an AI-assisted release readiness dashboard for monoliths and
microservice repositories. It helps engineering teams answer one important
question:

> Is this release safe, and what evidence supports that decision?

ReleasePilot is a compact decision cockpit backed by a Go API and a
dependency-free HTML, CSS, and JavaScript frontend.

## MVP Scope

The product analyzes one release candidate and presents:

- A `GO`, `NEEDS VALIDATION`, or `NO-GO` recommendation
- A release health score and risk breakdown
- Required validation checks
- Evidence-backed risk findings
- Affected services and blast radius
- A rollback plan
- Generated release notes
- GitHub Actions or GitLab CI validation status
- Secret, `.env`, database migration, and schema/API contract signals
- Specialized risk-agent summaries for security, schema/migration, CI/test
  coverage, and performance
- Blast-radius visualization showing impacted services, signals, and files

ReleasePilot can fetch real repository history from GitHub and GitLab, inspect
changed files, read CI status, detect database/schema changes, scan for
credential exposure, and use OpenAI to generate an evidence-backed release
report. A deterministic analyzer remains available as a fallback when OpenAI is
not configured or unavailable.

## User Workflow

```mermaid
flowchart LR
    A[Open releases] --> B[Analyze release]
    B --> C[Choose repository and branches]
    C --> D[Run analysis]
    D --> E[Review decision cockpit]
    E --> F{Release decision}
    F -->|GO| G[Proceed]
    F -->|Needs validation| H[Complete checks]
    F -->|NO-GO| I[Fix blockers]
    H --> D
    I --> D
```

## Architecture

```mermaid
flowchart LR
    UI[HTML CSS JavaScript UI] --> API[Go HTTP API]
    API --> Providers[GitHub and GitLab APIs]
    Providers --> CI[GitHub Actions and GitLab CI]
    API --> OpenAI[OpenAI Responses API]
    API --> Analyzer[Release Analyzer]
    Analyzer --> Agents[Specialized Risk Agents]
    Analyzer --> Blast[Blast Radius Graph]
    Analyzer --> Decision[Release Decision]
    Analyzer --> Risks[Risk Findings]
    Analyzer --> Checks[Validation Checklist]
    Analyzer --> Impact[Affected Services]
    Analyzer --> Rollback[Rollback Plan]
    Analyzer --> Notes[Release Notes]
```

## Project Structure

```text
.
├── README.md
├── go.mod
├── main.go
├── main_test.go
└── web
    ├── app.js
    ├── index.html
    └── styles.css
```

## API

### `GET /api/health`

Returns the service health status.

### `GET /api/releases`

Returns the known release candidates.

### `GET /api/releases/{id}`

Returns the complete release readiness report for one release.

### `POST /api/analyze`

Creates a release analysis.

Example request:

```json
{
  "provider": "github",
  "repository": "acme/payment-platform",
  "baseBranch": "main",
  "releaseBranch": "release/v2.14.0"
}
```

### `GET /api/settings`

Returns safe configuration status. Stored access tokens and API keys are never
included in responses.

### `PUT /api/settings`

Stores GitHub, GitLab, and OpenAI credentials in server memory. Blank secret
fields preserve the currently configured secret.

### `GET /api/repositories?provider=github`

Returns repositories visible to the configured read-only provider token.

### `GET /api/history?provider=github&repository=owner/repo&ref=main`

Returns recent commit history for a repository ref.

### `GET /api/openai/models`

Returns models visible to the configured OpenAI key. The Settings page uses
this to populate model choices after an API key is configured.

### `GET /api/releases/{id}/pdf`

Downloads the release readiness report as a PDF.

## Run Locally

Requirements:

- Go 1.22 or newer

Start ReleasePilot:

```bash
OPENAI_API_KEY=your-key go run .
```

Open:

```text
http://localhost:8080
```

Run tests:

```bash
go test ./...
```

## Credential Security

- GitHub tokens should be fine-grained tokens with read-only repository
  `Contents` and `Metadata` permissions.
- GitLab tokens should use the `read_api` scope only.
- Provider tokens and OpenAI keys are write-only in the UI and never returned
  by the API.
- Credentials entered in Settings are persisted in the local state file for
  this development setup.
- `.releasepilot/` and env files are ignored by git.
- For production deployment, replace in-memory secret storage with a managed
  secret store.

## Local Persistence

Reports and Settings are persisted to:

```text
.releasepilot/store.json
```

Override this path for testing or deployment:

```bash
RELEASEPILOT_STORE=/secure/path/store.json go run .
```

## Current Limitations

- Repository analysis uses provider compare APIs and currently stores recent
  commits plus changed-file metadata.
- Runtime observability systems are not connected yet.
- Release approvals and deployment execution are intentionally excluded.
- Data is held in memory and resets when the server restarts.

## Next Milestones

1. Deepen compare diff parsing and repository metadata.
2. Detect service boundaries, dependencies, and owners.
3. Add specialized AI analysis agents.
4. Connect GitHub pull requests and CI results.
5. Add policy-driven release gates and audit history.
