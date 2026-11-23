# Delos harness — shared task list

Lead: Claude Code (LEAD). Brief: `docs/plans/delos-harness.md`. Baseline tag: `d0-baseline` (pushed; main @ `6e73783`). Integration base for D1/D2: `team/integration/d1-base` (= DL-S Bifrost go.mod work + LEAD planning docs). Nothing pushed beyond the tag.

Teams, ownership, and branches:

| Team | Owns | Branch | Status |
| --- | --- | --- | --- |
| LEAD | Plan, task list, gates, reports (`docs/plans/`, `docs/design/notes/` index) | `team/lead/d0` | active |
| DL-S | `go.mod`, `go.sum`, `.goreleaser.yaml`, `Dockerfile*`, `cmd/delos-gateway/main.go` wiring | `team/dl-s/d0-spike` | **D0 done** (`24c7844`) |
| DL-T | `tests/`, `Makefile` (test/bench targets), `.github/workflows/` | `team/dl-t/d0-baseline` → `team/dl-t/d1-d2-suites` | **D0 done** (`677650a`, `e188098`); D1/D2 suites launched |
| DL-G | `services/runtime/` except keys + migrations, `pkg/semconv/` | `team/dl-g/d1-gateway` | **D1 launched** |
| DL-K | `services/runtime/keys*`, `services/runtime/migrations`, `proto/keys/v1`, `cli/cmd/key*` | `team/dl-k/d2-keys` | **D2 launched** |
| R-REVIEW | Review every PR against the brief | — | **note 0001 approved**; PR reviews at PR time |

## Milestones

- **D0** — DL-T ∥ DL-S: **done**. **Gate G0: approved by Eric** (report: `docs/plans/spike-report-d0.md`).
- **D1** — DL-G + DL-T (gaps 1, 2, 3, 5, 6). **In flight** (note 0001 approved; DL-G launched).
- **D2** — DL-K + DL-T (gap 4). **In flight** (DL-K launched). **Gate G1** after D2.
- **D3–D5** — not started this round.

## D0 results

### DL-S — done (`team/dl-s/d0-spike`, `24c7844`)
- [x] Go bump to 1.27.4. [x] Bifrost core + governance/otel/logging at `d913ed8` (five submodules + `maximhq/mcp-go` replace).
- [x] `CGO_ENABLED=0` builds on all four targets. [x] Headless Claude Code turn through `PassthroughStream` (mock upstream). [x] All eight verify-in-spike rows answered.

### DL-T — done (`team/dl-t/d0-baseline`, `677650a`, `e188098`)
- [x] CI fails on skipped DB tests (`tests/skipguard` + `make test-require-db`). [x] `/v1/messages` baseline test. [x] Bench baseline p99 **2.36 ms**.
- [x] `GO_VERSION` `1.25` → `1.27.4` in `.github/workflows/{ci,release,nightly}.yaml` (`e188098`).

## D0 follow-ups still open

- [ ] **DL-S** (needs valid `sk-ant-` key): live catalog-price ≤1% check + real-provider headless turn → fold into D1 exit checks.
- [ ] **DL-S**: real syft SBOM diff (tooling not installed; currently approximated).
- [ ] **LEAD**: drop `D0_SPIKE_NOTES.md` / `D0_BASELINE_NOTES.md` scratch files from any merge.

## D1 / D2 (in flight)

- [x] **R-REVIEW** approved design note 0001 (`docs/design/notes/0001-gateway-on-bifrost.md`).
- [x] **LEAD** built integration base `team/integration/d1-base`; all D1/D2 teams branch from it.
- [ ] **DL-G**: gateway on Bifrost per note 0001 (gaps 1, 2, 3, 5, 6).
- [ ] **DL-K**: keys on Bifrost governance (gap 4) — migrate `dk_` keys, thin `KeyService` gRPC, CLI.
- [ ] **DL-T**: swap-parity, agent e2e, key-lifecycle suites; re-bench with Bifrost inside.

Keys needed later (not now): valid `ANTHROPIC_API_KEY` (`sk-ant-…`) and `OPENAI_API_KEY` for D1 exit checks (live Claude Code/Codex sessions, ≤1% cost). Cassettes/mocks run keyless.

## Rules every team follows
- Branch from `team/integration/d1-base`; commit on `team/<id>/<topic>`; do **not** push (LEAD pushes after review).
- One gap per PR, tests + docs each. Mark runnable doc blocks `# docs-test`.
- Secrets env-only; never commit keys. `rg`, not `grep`.
- Design note before any wire / DB schema / config-surface change; R-REVIEW approves before code lands.
- Where this document and the code disagree, the code wins; record the difference.

## Gates
- **G0** — approved (Eric).
- **G1** — after D2; LEAD stops and reports.
