# Delos harness — shared task list

Lead: Claude Code (LEAD). Brief: `docs/plans/delos-harness.md`. Baseline tag: `d0-baseline` (pushed; main @ `6e73783`).

Teams, ownership, and branches:

| Team | Owns | Branch | Status |
| --- | --- | --- | --- |
| LEAD | Plan, task list, gates, reports (`docs/plans/`, `docs/design/notes/` index) | `team/lead/d0` | active — D1 kickoff |
| DL-S | `go.mod`, `go.sum`, `.goreleaser.yaml`, `Dockerfile*`, `cmd/delos-gateway/main.go` wiring | `team/dl-s/d0-spike` | **D0 done** (`24c7844`) |
| DL-T | `tests/`, `Makefile` (test/bench targets), `.github/workflows/` | `team/dl-t/d0-baseline` | **D0 done** (`677650a`); GO_VERSION follow-up routed |
| DL-G | `services/runtime/` except keys, `pkg/semconv/` | `team/dl-g/<topic>` | D1 — pending R-REVIEW note approval |
| DL-K | `services/runtime/keys*`, `proto/keys/v1`, `cli/cmd/key*`, key migrations | `team/dl-k/<topic>` | D2 — pending |
| R-REVIEW | Review every PR against the brief | — | reviewing note 0001 now |

## Milestones

- **D0** — DL-T ∥ DL-S: **done**. **Gate G0: approved by Eric** (report: `docs/plans/spike-report-d0.md`).
- **D1** — DL-G + DL-T (gaps 1, 2, 3, 5, 6). Kickoff: design note 0001 drafted (`docs/design/notes/0001-gateway-on-bifrost.md`), R-REVIEW reviewing; DL-T GO_VERSION bump routed.
- **D2** — DL-K + DL-T (gap 4). Gate G1 after D2.
- **D3–D5** — not started this round.

## D0 results

### DL-S — done (`team/dl-s/d0-spike`, `24c7844`)
- [x] Go bump to 1.27.4 (no `toolchain` directive needed).
- [x] Bifrost core + governance/otel/logging pinned at `d913ed8` (five submodules + `maximhq/mcp-go` replace).
- [x] `CGO_ENABLED=0` builds on all four targets (plain + `-tags=bifrost`).
- [x] Headless Claude Code turn through Bifrost `PassthroughStream` (mock upstream; real key blocked).
- [x] All eight verify-in-spike rows answered (see report §2).

### DL-T — done (`team/dl-t/d0-baseline`, `677650a`)
- [x] CI fails on skipped DB tests (`tests/skipguard` + `DELOS_TEST_REQUIRE_DB=1`; `make test-require-db`).
- [x] `/v1/messages` baseline test (`tests/conformance/messages_fidelity_test.go`).
- [x] Pre-Bifrost bench baseline: p99 **2.36 ms** (PASS vs 5 ms).

## D0 follow-ups

- [ ] **DL-T (routed)**: bump `GO_VERSION` `1.25` → `1.27.4` in `.github/workflows/{ci,release,nightly}.yaml`.
- [ ] **DL-S** (needs valid `sk-ant-` key): finish live catalog-price ≤1% check + a real-provider headless turn — fold into D1 exit checks.
- [ ] **DL-S**: generate a real syft SBOM diff (tooling not installed; currently approximated).
- [ ] **LEAD**: drop `D0_SPIKE_NOTES.md` / `D0_BASELINE_NOTES.md` scratch files from any merge.

## D1 kickoff (gaps 1, 2, 3, 5, 6)

- [ ] **R-REVIEW**: approve design note 0001 (in flight).
- [ ] **LEAD**: on approval, launch DL-G (gateway on Bifrost) and DL-K (D2 keys) in parallel, DL-T writing suites alongside.
- [ ] **LEAD**: decide D1 integration base (stack on `team/dl-s/d0-spike` go.mod work) and record it here.
- Keys needed later (not now): valid `ANTHROPIC_API_KEY` (`sk-ant-…`) and `OPENAI_API_KEY` for D1 exit checks (live Claude Code/Codex sessions, ≤1% cost). Cassettes/mocks run keyless.

## Rules every team follows
- Branch from `d0-baseline`; commit on `team/<id>/<topic>`; do **not** push (LEAD pushes after review).
- One gap per PR, tests + docs each. Mark runnable doc blocks `# docs-test`.
- Secrets env-only; never commit keys. `rg`, not `grep`.
- Design note (`docs/design/notes/NNNN-*.md`) before any wire / DB schema / config-surface change; R-REVIEW approves the note before code lands.
- Where this document and the code disagree, the code wins; record the difference.

## Gates
- **G0** — approved (Eric).
- **G1** — after D2; LEAD stops and reports.
