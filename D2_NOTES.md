# DL-K D2 scratch notes (branch-local, not for merge)

## What landed

- `proto/keys/v1/keys.proto` — KeyService: MintKey, RevokeKey, GetKeyUsage, ListKeys.
- `gen/go/keys/v1/` — generated (gitignored; `buf generate`).
- `services/runtime/keys_bifrost.go` — Bifrost configstore-backed KeyServiceStore + MemoryKeyServiceStore + session context metadata constants + `OpenKeyServiceStore` (opens Bifrost config store, applies Delos key migrations, runs the dk_ copy).
- `services/runtime/keys_grpc.go` — KeyService gRPC handler.
- `services/runtime/keys_migrations.go` — `KeysMigrations` embed + `MigrateLegacyKeys` (non-destructive, idempotent).
- `services/runtime/migrations/keys/003_migrate_dk_keys.{up,down}.sql` — key_migration ledger.
- `cli/cmd/key.go` — rewritten to call the control-plane KeyService (create/list/revoke/usage).
- `controlplane/controlplane.go` — wires KeyService, adds "keys" health name.
- `docs/design/notes/0002-keys-on-bifrost-governance.md` — design note (draft; LEAD routes R-REVIEW).

## Proved

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet -p 1 ./...` — exit 0.
- `CGO_ENABLED=0 go test -p 1 ./...` (DELOS_TEST_DSN → isolated postgres:15 on :55432) — exit 0, no skips.
- `TestMemoryKeyServiceStoreContract` + `TestBifrostKeyStoreContract` hold memory and Bifrost stores to one contract (mint/get/revoke/missing/usage/unscoped/invalid-budget, model scope, expiry).
- `TestMigrateLegacyKeys` proves the dk_ copy is non-destructive (source row survives), recorded (key_migration row), and idempotent (2nd run migrates 0).

## Gotchas hit

- Bifrost configstore first-open migration is slow (>10s): tests use a 5m context for OpenBifrostKeyStore, not the 10s ping context.
- Empty VK `name` violates the unique `governance_virtual_keys.name` index — Bifrost store defaults it to `key-<id8>` (same as memory store).
- `MintKey` must re-read the persisted VK after activation so the returned record carries budgets/rate limit (GetVirtualKey preloads them).
- VK `rate_limit_id` FK means deleting a VK does NOT delete its rate limit; test cleanup deletes rate limits explicitly.

## Open / coordination (see note 0002)

- Revoke/expiry <1s propagation: Bifrost OSS governance loads keys once at init (no poll) — DL-G must re-check per request or poll.
- Migrated dk_ keys are inactive (argon2id secret unrecoverable) — needs operator rotation; flag to LEAD/Eric.
- Model scoping stays Delos-side (Bifrost scopes per provider config; control plane doesn't know gateway providers).
