# Vigil semantics (v0)

What each field means, how `overall` is built, and which rule lives where. Each rule is stated once. The right-hand column names the fixture under `testdata/summary/` that pins it.

Vigil is a query surface. It combines the verdicts of named **authorities** into one document, `vigil.summary/v0`. It never probes a host, never takes a URL, and never derives severity from raw data.

## 1. Verdicts and `overall`

An authority is a tool that publishes its own verdict document: Simplex now, Augur once its Vigil fields land. `verdicts[]` holds one entry per configured authority: `source`, `state` (`ok`, `stale`, `unavailable`), `severity` as the authority published it (null when unavailable), `reasons[]`, `observed_at` (the authority document's `generated_at`, null when no document could be read). A verdict's `reasons[]` carries only Vigil's own codes (`source_stale`, `authority_unavailable`). Simplex publishes no top-level reasons, so Vigil does not invent a verdict reason from its host reasons; those travel in `hosts[].reasons`.

Severity order is GREEN < YELLOW < RED.

| Rule | Statement | Fixture |
| --- | --- | --- |
| Worst of | `overall` is the worst severity among available verdicts, after the two caps below. One authority: `overall` equals that authority's own `overall`, byte for byte. | `simplex-green`, `simplex-yellow-augur-red` |
| Stale cap | A verdict whose `generated_at` is older than `stale_after` (default 120s, per-authority override) has state `stale` and counts as at least YELLOW. GREEN rises to YELLOW; YELLOW and RED stay. The verdict keeps the severity the authority published and gains the reason `source_stale`. This is the only change Vigil makes to a verdict. | `simplex-stale-green` |
| Missing authority | An authority that is absent, unreadable, undecodable or on an unknown major has state `unavailable`, severity null, reason `authority_unavailable`. It leaves the worst-of, and `overall` cannot be better than YELLOW while it is missing. The cause goes in that source's `detail_code` (`source_unavailable`, `schema_major`). | `augur-unavailable` (Simplex green, Augur absent), `schema-major-mismatch` (Simplex on an unknown major, Augur green) |
| Nothing readable | If no authority could be read, `overall` is null. Null is never GREEN. | `all-unavailable` |
| No second rollup | Adding an authority never lowers `overall`. Vigil does not recompute a host or check result from a primitive. | `simplex-yellow-augur-red` |

Before Augur's Vigil fields land, `verdicts[]` holds `simplex` only, and the document says so. Augur-shaped fields injected early leave `overall` unchanged.

`hosts[]` comes from Simplex alone (`source: simplex`). Augur's per-host alerts live in the `augur{}` block. Vigil never merges the two into one host colour.

## 2. Vantage

`vantage` is carried from Simplex: `id` and `status` (`ok`, `blind`, `unchecked`). A blind vantage means the prober was cut off from the mesh. Simplex reports RED, marks `vantage.status` blind, puts `vantage_blind` on each host's `reasons`, and exits 5. That is a verdict, not an unavailable source: the document is readable, so `overall` is RED and Vigil exits 2. Vigil's exit code is its own, not the producer's. Fixture: `vantage-blind`.

## 3. Beside `overall`

These never change `overall`, the chip colour, or the exit code. Their status lives in `sources[]` only.

| Source | What it adds | Fixture |
| --- | --- | --- |
| Scry | `scry{}` carries counts and reasons (no node names, no server details). `observed:false` has no severity and makes the source `unavailable`. Scry is not an authority, so this never triggers the missing-authority cap. | `scry-unobserved` |
| Cubit | `cubit{}` is informational disk data. | (reserved block) |
| Grafana | A source. Down means `sources[]` shows it down and nothing else moves. | `grafana-down` |
| Prometheus | Named queries and targets for the CLI. Not an input to `overall`. | n/a |
| Signals | Ephemeral records from authenticated callers. | `signal-present` |

A source joins `verdicts[]` only if it ships a document with its own heartbeat and freshness and is ruled to count.

## 4. Reasons

Authorities own their reason strings. Vigil passes them through unchanged on `hosts[].reasons` (and in the `augur{}` block once Augur's fields land) and checks only the shape `^[a-z][a-z0-9_]{0,47}$`, so an upstream addition never breaks it. Vigil's own reasons are `source_stale`, `source_unavailable`, `schema_major`, `clock_skew` (a file newer than its own `generated_at`) and `authority_unavailable`. A consumer shows an unknown reason as plain text.

## 5. Exit codes

Codes 0 to 2 are verdicts. Codes 3 and up are errors, and no error path ever exits 0, 1 or 2. Only that split is shared across the family; each binary documents its own table.

| Code | Meaning |
| --- | --- |
| 0 | `overall` GREEN |
| 1 | `overall` YELLOW (a stale or missing authority is at least this) |
| 2 | `overall` RED |
| 3 | usage or configuration error |
| 4 | unavailable: `overall` null, or a backend is unreachable |
| 5 | auth, startup or bind failure |
| 6 | internal error (a recovered panic) |

The table is `internal/exitcode.Table`; a test fails if this page and the code disagree. Each summary golden carries its expected code in `expected_exit`. That is always Vigil's code, never the producer's: `vantage-blind` is 2 while Simplex exited 5, and `simplex-yellow-augur-red` is 2 while Simplex exited 1.

## 6. Routes and auth (`vigil serve`)

| Route | Auth |
| --- | --- |
| `GET /healthz` | none; returns `{"status":"ok"}` only |
| `GET /v0/summary`, `/v0/hosts`, `/v0/hosts/{id}`, `/v0/sources`, `/v0/signals` | read token |
| `POST /v0/signals` | write token |

There is no `/v0/query`, no `/v0/queries`, and no route that takes a URL, a selector or PromQL. Every non-200 is a `vigil.error/v0` document, and responses are JSON only.

- **Bind.** Default `127.0.0.1:10082`. A non-loopback bind needs `--allow-nonloopback` and one explicit address. Wildcard binds (`0.0.0.0`, `::`, empty) are always rejected, exit 5. A bind failure exits 5 and names the address. Port 10082 clears the Prometheus exporter allocations and the Simplex metrics port.
- **Tokens.** The read token (`VIGIL_READ_TOKEN_FILE`) is required on every `/v0/*` route on any bind, so a proxy in front of loopback cannot bypass it. The write token (`VIGIL_SIGNAL_TOKEN_FILE`) is required for `POST /v0/signals`. Facet holds the read token only. Token files must be mode `0600` or tighter; a missing file or a looser mode exits 5 at start. Secrets come from paths or the environment, never from argv.
- **Comparison.** Tokens are hashed with SHA-256, then compared in constant time. A missing token and a wrong token get byte-identical 401 bodies with no `WWW-Authenticate` header.
- **Transport.** Plain HTTP; the tailnet carries it. No TLS in v0.

## 7. Named queries

`vigil prom query <name>` runs a query from `queries[]` in the config. Free-form PromQL exists nowhere.

- The host slug is the only template variable. Instant queries only.
- Timeout 5s by default, 10s at most, sent as the API `timeout` parameter and enforced by a client deadline.
- `max_series` defaults to 200, and the request asks for one more; more results than the cap is the error `series_cap`.
- Any Prometheus `warnings` is the error `query_warnings`. A partial answer is never rendered.
- Response bodies are capped at 8 MiB, two requests in flight per backend, no redirects.
- No silent failover. Every result carries `backend` and `vantage`.
- `vigil prom targets` prints slug, `health` and `lastScrape`. It never prints `scrapeUrl`, `lastError` or `discoveredLabels`; an unmapped instance prints as `unmapped`.

## 8. Signals

A `vigil.signal/v0` is ephemeral and arrives only by authenticated POST.

- `severity` is `info` or `notice`, never GREEN, YELLOW or RED, so no reader mistakes a signal for a verdict.
- The body is at most 4 KiB, decoded strictly. `note` is at most 280 bytes with no control characters. `ttl` is clamped to 60s to 1h, default 5m. `created_at` comes from the server clock.
- The store is a 256-entry in-memory ring, pruned on read and on a timer, never persisted. Rate limit: 10 per second per remote address; loopback is exempt.
- Signals never change `overall` or `sources[]`. A flood of them leaves both untouched. Fixture: `signal-present`.

## 9. Transports and freshness

- Vigil reads each authority's document from a file path in the config (`path_ref`; 4 MiB cap, one read per request, unknown fields ignored). It never runs the authority and never reads Simplex's `/metrics`.
- The writer publishes by temp file, fsync and atomic rename, and keeps the last good file if a run fails. Vigil ages that file into `stale`.
- Freshness is the age of the document's `generated_at`. A file modified time ahead of `generated_at` adds the reason `clock_skew`.
- Simplex, Augur and Scry all arrive this way. Vigil stays exec-free and holds no cluster credentials.
- Prometheus and Grafana are queried live and have no freshness bound; their state is `ok` or unreachable.
- Vigil queries the secondary Prometheus on lathe only. The primary's liveness reaches it through a named query on the peer series.

## 10. Fixture index

`testdata/summary/<case>/` holds `document.json` and `expected_exit`. No golden carries an `augur{}` block: its content is frozen when Augur's fields land, and Augur's status rides in `verdicts[]` and `sources[]`.

| Case | `overall` | Exit |
| --- | --- | --- |
| `simplex-green` | GREEN | 0 |
| `simplex-yellow-augur-red` | RED | 2 |
| `simplex-stale-green` | YELLOW | 1 |
| `augur-unavailable` | YELLOW | 1 |
| `all-unavailable` | null | 4 |
| `vantage-blind` | RED | 2 |
| `schema-major-mismatch` | YELLOW | 1 |
| `grafana-down` | GREEN | 0 |
| `scry-unobserved` | GREEN | 0 |
| `signal-present` | GREEN | 0 |

The last three are built on `simplex-green` and prove that Grafana, Scry and a signal leave `overall` where it was.

`schema-major-mismatch`: the Simplex verdict is `unavailable` with severity null, `observed_at` null and reason `authority_unavailable`; its `sources[]` entry has `detail_code` `schema_major`; Augur is `ok` GREEN beside it; `overall` is YELLOW.

`signal-present` cannot show a signal inside the summary, because the summary has no signals field by contract. Its `document.json` is therefore byte-identical to `simplex-green`, and the directory also holds `signal.json`, a `vigil.signal/v0` document. The assertion is that the summary built with that signal present equals the summary built without it.
