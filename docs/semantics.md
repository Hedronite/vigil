# Vigil semantics (v0)

What each field means, how `overall` is built, and which rule lives where. Each rule is stated once. The right-hand column names the fixture under `testdata/summary/` that pins it.

Vigil is a query surface. It combines the verdicts of named **authorities** into one document, `vigil.summary/v0`. It never probes a host, never takes a URL, and never derives severity from raw data.

## 1. Verdicts and `overall`

An authority is a tool that publishes its own verdict document: Simplex now, Augur once its Vigil fields land. `verdicts[]` holds one entry per configured authority: `source`, `state` (`ok`, `stale`, `unavailable`), `severity` as the authority published it (null when unavailable), `reasons[]`, `observed_at` (the authority document's `generated_at`, null when no document could be read). A verdict's `reasons[]` carries Vigil's own codes (`source_stale`, `authority_unavailable`, and the rest of section 4) plus any authority-level reasons the authority publishes, passed through unchanged. Simplex publishes none at top level, so today a Simplex verdict carries only Vigil's codes; Vigil does not invent a verdict reason from Simplex's host reasons, which travel in `hosts[].reasons`.

Severity order is GREEN < YELLOW < RED.

**The rule, exactly.** `overall` is the worst severity among the readable verdicts in `verdicts[]`, after two caps and nothing else. The stale cap: a stale verdict counts as at least YELLOW. The missing-authority cap: while any configured authority is unavailable, `overall` is at least YELLOW. If no authority is readable, `overall` is null.

| Rule | Statement | Fixture |
| --- | --- | --- |
| Worst of | `overall` is the worst severity among the readable verdicts, after the two caps below. With one authority and no cap in play, `overall` is that authority's published severity. With two, it is the worse of the two. | `simplex-green` (one authority, GREEN), `vantage-blind` (one authority, RED), `simplex-yellow-augur-red` (YELLOW and RED give RED) |
| Stale cap | A verdict whose `generated_at` is older than `stale_after` (default 120s, per-authority override) has state `stale` and counts as at least YELLOW. GREEN rises to YELLOW; YELLOW and RED stay. The verdict keeps the severity the authority published and gains the reason `source_stale`; `overall` carries the capped value. So one authority does not always give `overall` equal to its published severity: a stale GREEN gives YELLOW. This is the only change Vigil makes to a verdict. | `simplex-stale-green` (verdict GREEN, `overall` YELLOW) |
| Missing authority | An authority that is absent, unreadable, undecodable or on an unknown major has state `unavailable`, severity null, reason `authority_unavailable`. It leaves the worst-of, and `overall` cannot be better than YELLOW while it is missing. The cause goes in that source's `detail_code` (`source_unavailable`, `schema_major`). | `augur-unavailable` (Simplex green, Augur absent), `schema-major-mismatch` (Simplex on an unknown major, Augur green) |
| Nothing readable | If no authority could be read, `overall` is null. Null is never GREEN. | `all-unavailable` |
| Adding an authority | Adding a configured authority can keep `overall` or make it worse, never better: the worst-of can only rise, and an authority that cannot be read adds the YELLOW floor of the missing-authority cap. | `simplex-yellow-augur-red` (adding a RED Augur to a YELLOW Simplex gives RED), `augur-unavailable` (adding an unreadable Augur to a GREEN Simplex gives YELLOW) |
| No second rollup | Vigil does not recompute a host or check result from a primitive. It combines verdicts; it does not re-derive them. | `simplex-yellow-augur-red` |

Before Augur's Vigil fields land, `verdicts[]` holds `simplex` only, and the document says so. Augur-shaped fields injected early leave `overall` unchanged.

`hosts[]` comes from Simplex alone (`source: simplex`). Augur's per-host alerts live in the `augur{}` block. Vigil never merges the two into one host colour.

## 2. Vantage

`vantage` is carried from Simplex: `id` and `status` (`ok`, `blind`, `unchecked`). A blind vantage means the prober was cut off from the mesh. Simplex reports RED, marks `vantage.status` blind, puts `vantage_blind` on each host's `reasons`, and exits 5. That is a verdict, not an unavailable source: the document is readable, so `overall` is RED and Vigil exits 2. Vigil's exit code is its own, not the producer's. Fixture: `vantage-blind`. When Simplex is unavailable there is no vantage to carry, so `vantage` is omitted; Vigil never invents one. Fixtures: `all-unavailable`, `schema-major-mismatch`.

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

Authorities own their reason strings. Vigil passes them through unchanged on `hosts[].reasons`, on `verdicts[].reasons` for any authority-level reasons an authority publishes (Simplex publishes none; Augur will), and in the `augur{}` block once Augur's fields land. It checks only the shape `^[a-z][a-z0-9_]{0,47}$`, so an upstream addition never breaks it. Vigil's own reasons are `source_stale`, `source_unavailable`, `schema_major`, `clock_skew` (a file newer than its own `generated_at`) and `authority_unavailable`. A consumer shows an unknown reason as plain text.

## 5. Exit codes

Codes 0 to 2 are verdicts. Codes 3 and up are errors, and no error path ever exits 0, 1 or 2. Only that split is shared across the family; each binary documents its own table.

| Code | Meaning |
| --- | --- |
| 0 | verdict GREEN |
| 1 | verdict YELLOW (a stale source is at least this) |
| 2 | verdict RED |
| 3 | usage or configuration error |
| 4 | source unavailable or backend unreachable |
| 5 | auth, startup or bind failure |
| 6 | internal error (panic recovered) |

The table is `internal/exitcode.Table`; a test fails if this page and the code disagree. Each summary golden carries its expected code in `expected_exit`. That is always Vigil's code, never the producer's: `vantage-blind` is 2 while Simplex exited 5, and `simplex-yellow-augur-red` is 2 while Simplex exited 1.

## 6. Routes and auth (`vigil serve`)

| Route | Auth |
| --- | --- |
| `GET /healthz` | none; returns `{"status":"ok"}` only |
| `GET /v0/summary`, `/v0/hosts`, `/v0/hosts/{id}`, `/v0/sources`, `/v0/signals` | read token |
| `POST /v0/signals` | write token |

There is no `/v0/query`, no `/v0/queries`, and no route that takes a URL, a selector or PromQL. Every non-200 is a `vigil.error/v0` document, and responses are JSON only.

- **Bind.** Default `127.0.0.1:10082`. A non-loopback bind needs `--allow-nonloopback` and one explicit address: an IPv4 literal or a bracketed IPv6 literal, with a port. Wildcard binds are always rejected, in every spelling: an empty host; any host made only of `0`, `.`, `:`, `[` and `]` (`0.0.0.0`, `[::]`, `[0::]`, `[::0]`, and the zero-padded and fully expanded forms); and the IPv4-mapped unspecified address in any spelling (`[::ffff:0.0.0.0]`, `[::ffff:0:0]`, any case). At runtime a wildcard `listen` exits 5 and names the address, wherever it came from: the config file or any flag. The `listen` key in `vigil.config.v0` also rejects the same set, but that is an authoring and CI check (editors, conformance tests), not an exit path: the binary never runs the JSON Schema. Exit 3 stays for malformed or strictly-decoded config errors. In YAML, quote a bracketed IPv6 value (`listen: "[::1]:10082"`): unquoted, the brackets open a flow sequence and the decoder fails before the schema runs. A bind failure exits 5 and names the address. Port 10082 clears the Prometheus exporter allocations and the Simplex metrics port.
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
- The Simplex writer publishes its document on every exit that carries a verdict or a blind vantage: 0, 1, 2 and 5. It never writes on 3 (configuration error) or 4 (I/O error); with no file there is nothing to read, so Vigil reports that authority `unavailable`. The vendored Simplex fixtures show it: `testdata/simplex/config-error` (exit 3) and `testdata/simplex/io-error` (exit 4) hold an `exit` file and no document; every other case holds both.
- The writer of record is `simplex serve --out PATH`, which rewrites the file every interval (default 30s, well inside Vigil's 120s `stale_after`). The fallback is a timer that runs `simplex check --json --out PATH`.
- Prometheus and Grafana are queried live and have no freshness bound; their state is `ok` or unreachable.
- Vigil queries the secondary Prometheus on lathe only. The primary's liveness reaches it through a named query on the peer series.

## 10. Versioning

Every document carries an `apiVersion`: `vigil.summary/v0`, `vigil.signal/v0`, `vigil.error/v0`, `vigil.config/v0`, `vigil.overlay/v0`. Inside `v0` the change rule is additive: a field may be added, and never removed, renamed, re-typed or given a new meaning. Anything else needs a new major. A reader ignores fields it does not know, and refuses a major it does not know.

- Vigil reading an authority document on an unknown major treats that authority as `unavailable`: severity null, reason `authority_unavailable`, `detail_code` `schema_major`. The caps in section 1 then apply. Fixture: `schema-major-mismatch`.
- A consumer of `vigil.summary` (Facet, an agent) refuses a summary on an unknown major instead of guessing at its fields.
- The five schemas under `schema/` are the contract. A conformance test validates every fixture against them, and the binary never runs the schema validator.

## 11. Configuration

`vigil.config/v0` is YAML and strict. The schema `schema/vigil.config.v0.schema.json` is the key list: an unknown key at any level is refused, and so is an unknown `apiVersion` major. Both are configuration errors, exit 3. Fixtures: `testdata/config/invalid/unknown-key.yaml` and `testdata/config/invalid/wrong-major.yaml`.

The top-level keys are:

| Key | Meaning |
| --- | --- |
| `apiVersion` | required; `vigil.config/v0` |
| `authorities` | required, at least one: `simplex` or `augur`, each with a `path_ref` and an optional `stale_after` |
| `listen` | the bind address (section 6) |
| `backends` | named backends, each an `id` and a `kind` (`prom` or `grafana`) and nothing else |
| `sources` | informational documents: `scry` or `cubit`, each with a `path_ref` |
| `queries` | named queries (section 7) |
| `freshness` | `stale_after`, default 120s |
| `tokens` | `read_file` and `signal_file`, the token file locations (section 6) |

Named backends and named queries come from config only. No config field takes a URL, an address or a selector: a backend's address lives in the off-repo overlay (`vigil.overlay/v0`), keyed by the backend ids declared here. A locator (`path_ref`, a token file) is an environment reference (`env:NAME`) or an absolute path with no traversal. Fixtures for the refusals: `testdata/config/invalid/no-authorities.yaml`, `authority-unknown.yaml`, `query-url.yaml`, `path-ref-traversal.yaml`.

## 12. Fixture index

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
