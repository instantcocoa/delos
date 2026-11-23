# D0 baseline notes (DL-T) — branch-local scratch, NOT for merge

Branch: `team/dl-t/d0-baseline` (cut from tag `d0-baseline` = main `6e73783`).
Owner: DL-T. These are raw findings and command outputs backing the D0 report to LEAD.
Delete before any merge; it exists only on this branch.

## Environment

- Go: `go1.26.10 linux/amd64`
- Docker: available (server 29.5.1). Host port `127.0.0.1:5432` is already occupied by an
  unrelated `langfuse-postgres-1` container, so Delos tests cannot reach `delos_test` there.
  For the DB-available proofs I ran a throwaway `postgres:15-alpine` on the loopback alias
  `127.0.0.2:5432` (the whole 127/8 is on `lo`, so the alias needs no extra config) and set
  `POSTGRES_HOST=127.0.0.2`. Container was removed afterwards.
- Generated proto code (`gen/`) is gitignored; it must be generated with `buf generate`
  before the module compiles (`services/prompt` imports `gen/go/prompt/v1`).

## Task 1 — fail CI when DB tests skip

### Mechanism chosen

A `go test -json` wrapper, `tests/skipguard/main.go` (new file, DL-T-owned). It:

- runs `go test -json -count=1 <args...>` (default `./...`),
- re-emits the human-readable output lines,
- watches `Action:"skip"` events whose `Package` is one of the three Postgres-backed
  packages (full import paths):
  - `github.com/instantcocoa/delos/pkg/database`
  - `github.com/instantcocoa/delos/services/prompt`
  - `github.com/instantcocoa/delos/services/runtime`
- when `DELOS_TEST_REQUIRE_DB=1` and any such skip occurred, exits 1 and prints the list;
  otherwise it propagates `go test`'s own exit code and only warns about the skips.

No cross-team file edits were needed: the skip sites in
`services/runtime/keys_postgres_test.go`, `services/prompt/store_test.go` and
`pkg/database/database_integration_test.go` are untouched. The tool detects their
`t.Skipf("PostgreSQL not available: ...")` skips from the JSON stream instead.

### Trigger in CI

`.github/workflows/ci.yaml` `test` job: the `Run tests` step now runs

    go run ./tests/skipguard \
      -race -coverpkg=./... -coverprofile=coverage.out -covermode=atomic ./...

with `DELOS_TEST_REQUIRE_DB: '1'` added to the step env. (The job already provides
Postgres as a service with `POSTGRES_DB: delos_test`.)

### Local dev still skips

`make test` / `make test-unit` are unchanged and still allow skips. New Makefile target
`make test-require-db` reproduces the CI gate locally.

### Proof

Failure path (actual, no `delos_test` on `127.0.0.1:5432`):

    $ DELOS_TEST_REQUIRE_DB=1 go run ./tests/skipguard ./services/prompt
    ...
    skipguard: 8 database-backed test(s) skipped:
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_Create_Integration
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_Get_NotFound_Integration
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_GetBySlug_Integration
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_Update_Integration
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_Delete_Integration
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_Create_DuplicateSlug_Integration
      - github.com/instantcocoa/delos/services/prompt → TestPostgresStore_Delete_NotFound_Integration
      - github.com/instantcocoa/delos/services/prompt → TestNewStore_PostgresBackend_Integration
    skipguard: DELOS_TEST_REQUIRE_DB=1 - failing because database-backed tests did not run ...
    (exit 1)

Pass path (throwaway Postgres on `127.0.0.2:5432`):

    $ POSTGRES_HOST=127.0.0.2 DELOS_TEST_REQUIRE_DB=1 go run ./tests/skipguard \
        ./services/prompt ./services/runtime ./pkg/database
    (exit 0 — every TestPostgresStore_* / TestPostgresKeyStoreContract /
     TestConnect_Integration / TestMigrator_Up_Integration ran and PASSed, no SKIP lines)

Full end-to-end target:

    $ POSTGRES_HOST=127.0.0.2 make test-require-db
    (exit 0 — all packages ok, including pkg/database, services/prompt, services/runtime)

### Gotcha found

`go run ./tests/skipguard -- <args>` does NOT forward args after `--` to the program
(the program receives none). Args after the package are forwarded only without `--`.
Both the Makefile and the CI workflow use the no-`--` form.

## Task 2 — today's /v1/messages behavior

New regression test: `tests/conformance/messages_fidelity_test.go`
(`TestAnthropicMessagesUnsupportedParams`). Hermetic: fake Anthropic Messages upstream
records exactly what the gateway forwards. Full conformance suite still green:

    $ go test ./tests/conformance/ -count=1
    ok  github.com/instantcocoa/delos/tests/conformance  3.264s

Behavior table (also pinned by the test):

| Field | Actual behavior today | docs/providers.md:95 | Verdict |
| --- | --- | --- | --- |
| top-level `thinking` | silently dropped (200 OK; not forwarded upstream) | "rejected with a structured 400 — never silently dropped" | diverges |
| `cache_control` on an input block | silently dropped (200 OK; not forwarded) | same promise | diverges |
| top-level `metadata` | silently dropped (200 OK; not forwarded) | same promise | diverges |
| `document` content block | structured 400 `invalid_request_error`: `messages[0]: unsupported content block type "document"`; no provider call | same promise | conforms (400), though as an unsupported block type |
| `anthropic-beta` header | silently dropped (200 OK; header not forwarded) | promise is about parameters; header is dropped silently | diverges from the "never silently dropped" spirit |

Recorded gateway response for the `document` case:

    {"type":"error","error":{"type":"invalid_request_error",
      "message":"messages[0]: unsupported content block type \"document\" (request id: ...)"}}

## Task 3 — pre-Bifrost bench baseline

    $ make bench
    over 2000 requests: gateway p50=734.438µs p95=3.316776ms p99=6.917466ms |
      direct-to-upstream p50=264.973µs p99=4.546439ms | p99 gateway overhead=2.371027ms
    --- PASS: TestGatewayOverheadP99 (3.64s)

Second run for stability:

    over 2000 requests: gateway p50=754.718µs p95=2.357576ms p99=4.562629ms |
      direct-to-upstream p50=285.703µs p99=2.219355ms | p99 gateway overhead=2.343274ms
    --- PASS: TestGatewayOverheadP99 (3.05s)

**Pre-Bifrost p99 gateway overhead: ~2.34–2.37 ms. Verdict: PASS** (gate is 5 ms; the
test measures gateway-vs-direct interleaved, so the number is gateway-over-direct).

## Plan deviations found (docs/plans/delos-harness.md)

- The brief says the branch `team/dl-t/d0-baseline` is "already pushed". It is not:
  `git ls-remote origin 'refs/heads/team/*'` returns nothing, and no local branch of that
  name existed. I created it locally from `d0-baseline^{commit}` (= `6e73783`). Nothing was
  pushed (per instructions).
- `d0-baseline` is an annotated tag: `git rev-parse d0-baseline` is the tag object
  `698753c…`; `d0-baseline^{commit}` is `6e73783` (= main).
- Gap #13 / D0 bullet says "fail CI when database tests skip" and names two skip sites
  (`services/runtime/keys_postgres_test.go`, `services/prompt/store_test.go`). There is a
  third DB-backed package with the same skip convention:
  `pkg/database/database_integration_test.go` (`TestConnect_Integration` etc.). skipguard
  covers all three.

## Commands (full list)

    buf generate
    go vet ./tests/skipguard/ ./tests/conformance/
    go test ./tests/conformance/ -run TestAnthropicMessagesUnsupportedParams -count=1 -v
    go test ./tests/conformance/ -count=1
    go build -o /tmp/skipguard ./tests/skipguard/
    DELOS_TEST_REQUIRE_DB=1 go run ./tests/skipguard ./services/prompt          # failure path
    docker run -d --name delos-skipguard-pg -p 127.0.0.2:5432:5432 \
        -e POSTGRES_USER=delos -e POSTGRES_PASSWORD=delos -e POSTGRES_DB=delos_test postgres:15-alpine
    POSTGRES_HOST=127.0.0.2 DELOS_TEST_REQUIRE_DB=1 go run ./tests/skipguard \
        ./services/prompt ./services/runtime ./pkg/database                     # pass path
    POSTGRES_HOST=127.0.0.2 make test-require-db                                # full target, exit 0
    make bench                                                                    # x2
    go build ./... && go vet ./...                                                # both clean
    docker rm -f delos-skipguard-pg
