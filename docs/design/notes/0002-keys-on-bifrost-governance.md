# 0002 — Keys on Bifrost governance (D2)

Status: **approved** (R-REVIEW, 2026-11-23). Team: DL-K. Surface: **wire + DB schema**. Gap closed: 4.

## Decision

Delos virtual keys move into Bifrost's governance config store (Postgres). The
control plane mints, revokes and reads usage through a thin `KeyService` gRPC
API (`proto/keys/v1`) over Bifrost's `framework/configstore` — the same tables
the gateway's Bifrost governance plugin (SDK mode, confirmed in the D0 spike)
reads for request-time enforcement. `delos key` CLI stops writing straight to
Postgres and calls the new API.

Bifrost owns budgets (reset intervals), rate limits and `expires_at`. Delos
keeps two things Bifrost cannot express for an agent client, recorded below:
model scoping (Bifrost scopes models per provider config, and the control plane
mints keys without knowing which providers the gateway registered) and the
session/org/subject attribution carried on each key.

## Key model mapping

| Delos `virtual_keys` today | Bifrost governance today | Notes |
| --- | --- | --- |
| `id` | `governance_virtual_keys.id` | preserved |
| `name` | `governance_virtual_keys.name` | preserved (unique) |
| `prefix` + `hash` (argon2id) | `governance_virtual_keys.value` + `value_hash` (SHA-256) | **The plaintext secret cannot be recovered from the argon2id hash**, so migrated keys are created **inactive** with a placeholder value (see Migration). New keys store the plaintext in Bifrost's `value` column; `value_hash` is computed by Bifrost's `BeforeSave`. |
| `models` (`TEXT[]`) | key `Metadata["delos.models"]` (JSON array) | kept Delos-side; see Model scoping |
| `token_budget` (monthly tokens) | `governance_rate_limits.token_max_limit` / `token_reset_duration` | token budgets become rate limits |
| `usd_budget` (monthly USD) | `governance_budgets.max_limit` / `reset_duration` | one budget per reset interval (daily, monthly, total) |
| `revoked` | `governance_virtual_keys.is_active = false` | — |
| — | `governance_virtual_keys.expires_at` | new; nil = never expires |
| — | key `Metadata["delos.session_id" / "delos.org_id" / "delos.subject" / "delos.labels"]` | new; session attribution |

## Migration

New SQL migration `services/runtime/migrations/keys/003_migrate_dk_keys.up.sql`
(+ `.down.sql`), embedded separately from `runtime.Migrations` so the
gateway's existing startup path is unchanged. It runs only after Bifrost's
config store has created the `governance_*` tables (the `BifrostKeyStore`
constructor applies it, see Wire-up).

For each row in `virtual_keys` the migration:

1. inserts a row into `governance_virtual_keys` with `is_active = false`,
   a placeholder `value` (the argon2id secret is unrecoverable), `expires_at`
   unset, and metadata carrying `delos.models`;
2. copies `usd_budget` (when > 0) into a monthly (`reset_duration '1M'`)
   `governance_budgets` row owned by the key;
3. copies `token_budget` (when > 0) into a monthly token rate limit;
4. records the mapping (`old id`, `old prefix`, `new id`) in a new ledger
   table `key_migration`.

It is non-destructive at the row level: `virtual_keys` and `key_usage` are
left untouched, the migration reads only, and the ledger preserves the
old-to-new mapping. Down deletes only the rows the up inserted (by the recorded
mapping), never the source tables.

**Consequence:** because every migrated key is created `is_active = false` with
an unrecoverable placeholder secret, **all existing `dk_` keys stop
authenticating the moment the up migration runs** and must be re-minted through
the hub/`KeyService`. The rows are recoverable (down), but the plaintext
secrets are gone for good. This migration therefore **gates on Eric's sign-off**
under the brief's "stop and ask before dropping data in a migration" rule.

## KeyService API (`proto/keys/v1`)

`MintKey`, `RevokeKey`, `GetKeyUsage` (plus the metadata needed to rebuild a
`delos key list` without a raw DB connection). `MintKeyRequest` carries
`session_id`, `org_id`, `subject`, `labels`, optional `expires_at`, optional
`models`, repeated `budgets` (`max_limit_usd` + `reset_duration`), and an
optional `rate_limit` (token and/or request max + reset). `MintKeyResponse`
returns the key once: `secret` plus the stored `key`.

`reset_duration` is a Bifrost duration string (`"30s"`, `"1h"`, `"1d"`,
`"1w"`, `"1M"`, `"1Q"`, `"1Y"`, …). An empty `reset_duration` means **total
(never resets within the key's life)** and is stored as `"1Y"` — Bifrost has no
"never reset" sentinel; a year is total for session keys, which live minutes to
hours.

`GetKeyUsage` reads Bifrost's `governance_budgets.current_usage` and
`governance_rate_limits.token_current_usage` as the source of truth; the legacy
`key_usage` table is kept read-only as history, not the source.

## Model scoping

Bifrost scopes models through `ProviderConfigs[].AllowedModels`, one config
per provider. The control plane mints a session key without knowing which
providers the gateway registered, and Delos keys were provider-agnostic model
lists, so model scoping stays Delos-side: the list lives in
`Metadata["delos.models"]` (JSON array) and the gateway enforces it at the thin
request layer with the existing `AllowsModel` rule (`""`/empty = all,
`provider/*` wildcards). This stays Delos-side because `governance_virtual_keys`
has no key-level model list — `AllowedModels` lives on
`TableVirtualKeyProviderConfig` (per provider, deny-by-default when empty, see
`virtualkey.go ProviderConfigs[].AllowedModels`) and the control plane mints keys
without knowing the gateway's registered providers. Budgets, rate limits, expiry
and revoke are all Bifrost.

## Session/org/subject context shape (handoff to DL-G)

A minted key's `Metadata` map carries, and the gateway must stamp on every
span it emits for that key:

| Metadata key | Span attribute | Meaning |
| --- | --- | --- |
| `delos.session_id` | `gen_ai.conversation.id` | the hub session id |
| `delos.org_id` | `delos.org_id` | the owning org |
| `delos.subject` | `delos.subject` | the acting subject (user/agent) |
| `delos.labels` | `delos.labels` | JSON object of free-form labels, for cost attribution |
| `delos.models` | — (enforced, not stamped) | model scope |

All metadata values are validated by Bifrost (≤50 entries, keys
`[a-zA-Z0-9._-]`, values ≤512 chars).

## Wire-up

`controlplane/controlplane.go` constructs the `BifrostKeyStore` when
`DELOS_STORAGE_BACKEND=postgres` (else a memory store for dev), registers the
`KeyService` handler on the gRPC server, and adds `keys` to the health names.
The store constructor opens Bifrost's config store (which runs Bifrost's own
migrations), then applies Delos's `keys` migration set.

## Exit criteria (D2)

- A session key stops working within 1 s of revoke and at `expires_at` on every
  gateway replica.
- Budget refusals are a structured 429 naming the key and the budget.

Both are enforced by the gateway's Bifrost governance wiring (DL-G), reading the
tables this note defines. See Open questions: Bifrost's OSS governance store
loads keys into memory once at init and has no built-in poll, so the gateway
re-reads key state from Postgres per request (no cache; see Open questions).

## Open questions

1. ~~Revoke/expiry propagation <1 s~~ — **resolved**: the gateway re-reads key
   state (`is_active`, `expires_at`) from Postgres per request (no cache),
   implemented by DL-G in `services/runtime/bifrost_auth.go`; budget/rate-limit
   counters stay on Bifrost's in-memory enforcement.
2. **Gate condition (Eric's sign-off required before the migration merges):**
   migrated `dk_` keys become inactive (their argon2id secret is unrecoverable)
   and operators must re-mint. Is "migrate to inactive + ledger" acceptable, or
   should migrated keys keep authenticating through the legacy argon2id path for
   one release?
