# Vigil semantics (v0)

What each field means, how `overall` is built, and which rule lives where. Each rule is stated once. The right-hand column names the fixture under `testdata/summary/` that pins it.

Vigil is a query surface. It combines the verdicts of named **authorities** into one document, `vigil.summary/v0`. It never probes a host, never takes a URL, and never derives severity from raw data.

## 1. Verdicts and `overall`

An authority is a tool that publishes its own verdict document: Simplex now, Augur once its Vigil fields land. `verdicts[]` holds one entry per configured authority: `source`, `state` (`ok`, `stale`, `unavailable`), `severity` as the authority published it (null when unavailable), `reasons[]`, `observed_at` (the authority document's `generated_at`, null when no document could be read). A verdict's `reasons[]` carries Vigil's own codes (`source_stale`, `authority_unavailable`, and the rest of section 4) plus any authority-level reasons the authority publishes, passed through unchanged. Simplex publishes none at top level, so today a Simplex verdict carries only Vigil's codes; Vigil does not invent a verdict reason from Simplex's host reasons, which travel in `hosts[].reasons`.

Severity order is GREEN < YELLOW < RED.

**The rule, exactly.** `overall` is the worst severity among the readable verdicts in `verdicts[]`, after three caps and nothing else. The stale cap: a stale verdict counts as at least YELLOW. The missing-authority cap: while any configured authority is unavailable, `overall` is at least YELLOW. The drift cap: while an authority has drifted from Vigil's declaration and the grace window has closed (section 20), `overall` is at least YELLOW. If no authority is readable, `overall` is null.

The schema holds each verdict to its state: `unavailable` has severity null, and `ok` and `stale` always have a severity.

| Rule | Statement | Fixture |
| --- | --- | --- |
| Worst of | `overall` is the worst severity among the readable verdicts, after the caps. With one authority and no cap in play, `overall` is that authority's published severity. With two, it is the worse of the two. | `simplex-green` (one authority, GREEN), `vantage-blind` (one authority, RED), `simplex-yellow-augur-red` (YELLOW and RED give RED) |
| Stale cap | A verdict whose document is not fresh (section 9; `stale_after` defaults to 120s, with a per-authority override) has state `stale` and counts as at least YELLOW. GREEN rises to YELLOW; YELLOW and RED stay. The verdict keeps the severity the authority published and gains the reason `source_stale`; `overall` carries the capped value. So one authority does not always give `overall` equal to its published severity: a stale GREEN gives YELLOW. This is the only change Vigil makes to a verdict. | `simplex-stale-green` (verdict GREEN, `overall` YELLOW) |
| Missing authority | An authority that is absent, unreadable, undecodable or on an unknown major has state `unavailable`, severity null, reason `authority_unavailable`. It leaves the worst-of, and `overall` cannot be better than YELLOW while it is missing. The cause goes in that source's `detail_code` (`source_unavailable`, `schema_major`). | `augur-unavailable` (Simplex green, Augur absent), `schema-major-mismatch` (Simplex on an unknown major, Augur green) |
| Nothing readable | If no authority could be read, `overall` is null. Null is never GREEN. | `all-unavailable` |
| Adding an authority | Adding a configured authority can keep `overall` or make it worse, never better: the worst-of can only rise, and an authority that cannot be read adds the YELLOW floor of the missing-authority cap. | `simplex-yellow-augur-red` (adding a RED Augur to a YELLOW Simplex gives RED), `augur-unavailable` (adding an unreadable Augur to a GREEN Simplex gives YELLOW) |
| Declaration drift | An authority's drift past the grace window raises `overall` to at least YELLOW and never to RED. It changes no severity, and it leaves a null `overall` null. An informational source's drift is listed and colours nothing. | section 20 |
| No second rollup | Vigil does not recompute a host or check result from a primitive. It combines verdicts; it does not re-derive them. | `simplex-yellow-augur-red` |

Until Augur's reader ships, a config lists `simplex` only, and `verdicts[]` then holds `simplex` only. A configured `augur` authority is not left out: it is `unavailable` with `source_unavailable`, so the missing-authority cap applies (test: `TestSimplexSummaryConfiguredAugur`). Augur-shaped fields injected early leave `overall` unchanged.

`hosts[]` comes from Simplex alone (`source: simplex`). Augur's per-host alerts live in the `augur{}` block. Vigil never merges the two into one host colour.

## 2. Vantage

`vantage` is carried from Simplex: `id` and `status` (`ok`, `blind`, `unchecked`). A blind vantage means the prober was cut off from the mesh. Simplex reports RED, marks `vantage.status` blind, puts `vantage_blind` on each host's `reasons`, and exits 5. That is a verdict, not an unavailable source: the document is readable, so `overall` is RED and Vigil exits 2. Vigil's exit code is its own, not the producer's. Fixture: `vantage-blind`. When Simplex is unavailable there is no vantage to carry, so `vantage` is omitted; Vigil never invents one. Fixtures: `all-unavailable`, `schema-major-mismatch`.

## 3. Beside `overall`

These never change `overall`, the chip colour, or the exit code, and their status lives in `sources[]` only. That holds for declaration drift too: an informational source on another watch declaration is listed in `declaration_drift` and never colours `overall` (section 20).

| Source | What it adds | Fixture |
| --- | --- | --- |
| Scry | `scry{}` carries counts and reasons (no node names, no server details). `observed:false` has no severity and makes the source `unavailable`. Scry is not an authority, so this never triggers the missing-authority cap. | `scry-unobserved` |
| Cubit | `cubit{}` is informational disk data. | (reserved block) |
| Grafana | A source. Down means `sources[]` shows it down and nothing else moves. | `grafana-down` |
| Prometheus | Named queries and targets for the CLI. Not an input to `overall`. | n/a |
| Signals | Ephemeral records from authenticated callers. | `signal-present` |

A source joins `verdicts[]` only if it ships a document with its own heartbeat and freshness and is ruled to count.

## 4. Reasons

Authorities own their reason strings. Vigil passes them through unchanged on `hosts[].reasons`, on `verdicts[].reasons` for any authority-level reasons an authority publishes (Simplex publishes none; Augur will), and in the `augur{}` block once Augur's fields land. It checks only the shape `^[a-z][a-z0-9_]{0,47}$`, so an upstream addition never breaks it. Vigil's own reasons are `source_stale`, `source_unavailable`, `schema_major`, `clock_skew` (section 9), `authority_unavailable` and `declaration_drift` (section 20). A consumer shows an unknown reason as plain text.

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
| `GET /v0/summary`, `/v0/hosts`, `/v0/hosts/{id}`, `/v0/sources` | read token |
| `GET` / `POST /v0/signals` (G3) | read token for GET; write token for POST |

There is no `/v0/query`, no `/v0/queries`, and no route that takes a URL, a selector or PromQL. Every non-200 is a `vigil.error/v0` document, and responses are JSON only.

- **Bind.** Default `127.0.0.1:10082`. A non-loopback bind needs `--allow-nonloopback` and one explicit address: an IPv4 literal or a bracketed IPv6 literal, with a port. Wildcard binds are always rejected, in every spelling: an empty host; any host made only of `0`, `.`, `:`, `[` and `]` (`0.0.0.0`, `[::]`, `[0::]`, `[::0]`, and the zero-padded and fully expanded forms); and the IPv4-mapped unspecified address in any spelling (`[::ffff:0.0.0.0]`, `[::ffff:0:0]`, any case). At runtime a wildcard `listen` exits 5 and names the address, wherever it came from: the config file or any flag. The `listen` key in `vigil.config.v0` also rejects the same set, but that is an authoring and CI check (editors, conformance tests), not an exit path: the binary never runs the JSON Schema. Exit 3 stays for malformed or strictly-decoded config errors. In YAML, quote a bracketed IPv6 value (`listen: "[::1]:10082"`): unquoted, the brackets open a flow sequence and the decoder fails before the schema runs. A bind failure exits 5 and names the address. Port 10082 clears the Prometheus exporter allocations and the Simplex metrics port.
- **Tokens.** The read token (`VIGIL_READ_TOKEN_FILE`) is required on every `/v0/*` route on any bind, so a proxy in front of loopback cannot bypass it. The write token (`VIGIL_SIGNAL_TOKEN_FILE`) is required for `POST /v0/signals`. Facet holds the read token only. Token files must be mode `0600` or tighter; a missing file or a looser mode exits 5 at start. Secrets come from paths or the environment, never from argv.
- **Comparison.** Tokens are hashed with SHA-256, then compared in constant time. A missing token and a wrong token get byte-identical 401 bodies with no `WWW-Authenticate` header. Callers send `Authorization: Bearer` with the raw token from the file.
- **G2 surface.** `/healthz` is open. `GET /v0/summary` matches `vigil simplex summary --json` for the same config, overlay validation and clock. `GET /v0/hosts` returns the summary `hosts[]`; `GET /v0/hosts/{id}` returns one host or `not_found`; `GET /v0/sources` returns the summary `sources[]` only and never dials a backend. `/v0/signals` and `/v0/testimony` are not registered until G3 and G4 (404 or 405).
- **Timeouts.** The listener sets bounded `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout` and `MaxHeaderBytes`. A recovered panic is a 500 `internal` with no stack.
- **Shutdown.** SIGINT and SIGTERM drain the listener and exit 0.
- **Transport.** Plain HTTP; the tailnet carries it. No TLS in v0.

## 7. Named queries

`vigil prom query <name> [--host <slug>]` runs a query from `queries[]` in the config. Free-form PromQL exists nowhere: not on the CLI, not on a route.

- **The name is the only handle.** It must match the shape of a configured name (`^[a-z][a-z0-9_]{0,47}$`). A URL, a selector or an expression in its place is a usage error, exit 3, before any file is read. Test: `TestURLShapedArgumentsExitUsage`.
- **One placeholder, one literal.** `--host` takes a slug, and the only substitution is the exact text `{{.host}}`, replaced by the first instance label the overlay lists for that slug (section 17): the backend holds instance labels, not slugs. Vigil replaces that text and runs no template engine. A slug the overlay does not list is exit 3 (`unknown_host`). Tests: `TestHostPlaceholderTakesTheFirstInstance` and `TestB3_HostPlaceholderUsesInstanceMap`. Config validation refuses any other `{{` in an `expr`. Fixture: `testdata/config/invalid/expr-second-variable.yaml`.
- **`--host` is checked before it reaches an expression.** It must be a slug (`^[a-z][a-z0-9-]{0,31}$`) that is listed in the overlay's `hosts[]`. No slug is built in, so a slug the overlay does not list is exit 3, the same as any other unknown value. An unchecked value would let a caller write PromQL through a label matcher.
- **The expression and the flag must agree.** An `expr` that names `{{.host}}`, run without `--host`, is exit 3 with the code `missing_host`. `--host` given to an `expr` with no placeholder is exit 3 with the code `unexpected_host`. There is no fan-out in V1: one run asks about one host or about the whole expression, never about every host in turn.
- **Instant queries only.** The request carries `query`, `time` (the pinned clock when `--now` is set), `timeout` and `limit`.
- **Deadline.** 5s by default and 10s at most, set per query in config as `5s` to `10s`. It is sent as the API `timeout` parameter and enforced by a client deadline. A longer value is refused at load. Fixture: `testdata/config/invalid/timeout-too-long.yaml`.
- **Series cap.** `max_series` defaults to 200 and may not exceed 200 (the config refuses a higher value). The request asks for `max_series + 1`, so a result that exceeds the cap is detected and fails as `series_cap`. Fixture: `testdata/config/invalid/max-series-too-high.yaml`.
- **An empty result is an answer.** A query that matches no series exits 0 with `series: []`. It is not an error and carries no detail code; `no_series` belongs to sources whose data is expected but absent.
- **Warnings.** Any `warnings` in a Prometheus answer fails the call as `query_warnings`. Neither a cap nor a warning ever produces a partial answer: the command prints nothing on stdout.
- **A rejected expression.** Prometheus answers `bad_data` for an expression it will not parse. That is a defect in the config, not in the backend, so it is `query_rejected` and exit 3.
- **No silent failover.** Every result carries `backend` and `vantage`. If the configured backend cannot be reached the command exits 4 and says so; it never asks another.
- **Output labels.** Only `host` and `job` are carried from the returned labels, and only in their scrubbed form (section 13).

Tests: `internal/promclient` `TestQueryRules` has one case for each rule above (a missing host, an unexpected host, an unknown host, a plain answer, an empty answer, a dotted sample value, a value that is not a number, the special values, the series cap, warnings, `bad_data`, a backend timeout, a refused redirect, the ignored proxy variables, the body cap and an unset backend). Recorded answers live in `internal/promclient/testdata/prom/`: `query-plain.json`, `query-empty.json`, `query-special-values.json`, `query-series-cap.json`, `query-warnings.json`, `query-bad-data.json` and `query-timeout.json`. The command layer is pinned by `TestPromQueryJSON` and `TestPromQueryHostRules` in `cmd/vigil`.

## 8. Signals

A `vigil.signal/v0` is ephemeral and arrives only by authenticated POST.

- `severity` is `info` or `notice`, never GREEN, YELLOW or RED, so no reader mistakes a signal for a verdict.
- The body is at most 4 KiB, decoded strictly. `note` is at most 280 bytes with no control characters. `ttl` is clamped to 60s to 1h, default 5m. `created_at` comes from the server clock.
- The store is a 256-entry in-memory ring, pruned on read and on a timer, never persisted. Rate limit: 10 per second per remote address; loopback is exempt.
- Signals never change `overall` or `sources[]`. A flood of them leaves both untouched. Fixture: `signal-present`.

## 9. Transports and freshness

- Vigil reads each `via: file` authority's document from `path_ref` (4 MiB cap, one read per request, unknown fields ignored). A `via: push` authority is filled from the testimony store when `vigil serve` is running; a one-shot `vigil simplex summary` reports it `unavailable` with `source_unavailable` and never opens a path for it. It never runs the authority and never reads Simplex's `/metrics`.
- The writer publishes by temp file, fsync and atomic rename, and keeps the last good file if a run fails. Vigil ages that file into `stale`.
- `received_at` is Vigil's own clock when it first saw a document with a new pair of `generated_at` and body digest; a file that does not change keeps it and ages. A one-shot read has no memory, so it takes the file's modification time, never later than now.
- A document is fresh only while both hold: `now - received_at <= eff_valid_for` and `now - generated_at <= eff_valid_for + skew_budget`. `eff_valid_for` is the smaller of the document's `valid_for` and `stale_after`, or `stale_after` when the document has no `valid_for`. Configuration can shorten a producer's promise and never extend it, and a producer clock that runs ahead buys no extra life.
- `clock_skew` is reported for a source when `received_at` and `generated_at` differ, in either direction, by more than `freshness.skew_budget` (default 10s). It is information: it never makes a stale document fresh and never colours `overall` by itself. It rides in that authority's `verdicts[].reasons`; a source row with state `ok` has no `detail_code`, so the row shows it only as the gap between `observed_at` and `received_at`.
- Simplex, Augur and Scry all arrive this way. Vigil stays exec-free and holds no cluster credentials.
- The Simplex writer publishes its document on every exit that carries a verdict or a blind vantage: 0, 1, 2 and 5. It never writes on 3 (configuration error) or 4 (I/O error); with no file there is nothing to read, so Vigil reports that authority `unavailable`. The vendored Simplex fixtures show it: `testdata/simplex/config-error` (exit 3) and `testdata/simplex/io-error` (exit 4) hold an `exit` file and no document; every other case holds both.
- The writer of record is `simplex serve --out PATH`, which rewrites the file every interval (default 30s, well inside Vigil's 120s `stale_after`). The fallback is a timer that runs `simplex check --json --out PATH`.
- Prometheus and Grafana are queried live and have no freshness bound; their state is `ok` or unreachable.
- Vigil queries the secondary Prometheus on the vantage host only. The primary's liveness reaches it through a named query on the peer series.

## 10. Versioning

Every document carries an `apiVersion`: `vigil.summary/v0`, `vigil.signal/v0`, `vigil.receipt/v0`, `vigil.error/v0`, `vigil.config/v0`, `vigil.overlay/v0`, `vigil.sources/v0`, `vigil.targets/v0`, `vigil.query/v0`, `vigil.health/v0` and `vigil.validate/v0`. Inside `v0` the change rule is additive: a field may be added, and never removed, renamed, re-typed or given a new meaning. Anything else needs a new major. A reader ignores fields it does not know, and refuses a major it does not know.

From V3 onward, `vigil.summary/v0` is **stable**: only additive changes within v0 are permitted, recorded here and in the summary schema description. `vigil.sources/v0` references `$defs/source` in the summary schema rather than copying it (section 3). A consumer that compiles `vigil.sources/v0` must register the summary schema as well as the sources schema, because the row shape is resolved through that `$ref`.

- Vigil reading an authority document on an unknown major treats that authority as `unavailable`: severity null, reason `authority_unavailable`, `detail_code` `schema_major`. The caps in section 1 then apply. Fixture: `schema-major-mismatch`.
- A consumer of `vigil.summary` (Facet, an agent) refuses a summary on an unknown major instead of guessing at its fields.
- The Vigil schemas under `schema/` are the contract: `vigil.summary`, `vigil.signal`, `vigil.receipt`, `vigil.error`, `vigil.config`, `vigil.overlay`, `vigil.sources`, `vigil.targets`, `vigil.query`, `vigil.health` and `vigil.validate`, each at `v0`. `simplex.probe.v0.schema.json` beside them is a vendored copy of Simplex's own contract, not a Vigil document. A conformance test validates every fixture against them, and the binary never runs the schema validator.
- Two pairs are held together by the schemas. A health document or a source row with state `ok` has no `detail_code` (absent or null), and an `unavailable` one names a code; a `stale` row may name `source_stale` or nothing. Every slug in a Vigil document starts with a letter, as in config and on the command line. Tests: `internal/schematest` `TestHealthStateDetailCoupling`, `TestSourceStateDetailCoupling`, `TestVerdictStateSeverityCoupling` and `TestDocumentSlugsStartWithALetter`.

## 11. Configuration

`vigil.config/v0` is YAML and strict. The schema `schema/vigil.config.v0.schema.json` is the key list: an unknown key at any level is refused, and so is an unknown `apiVersion` major. Both are configuration errors, exit 3. Fixtures: `testdata/config/invalid/unknown-key.yaml` and `testdata/config/invalid/wrong-major.yaml`.

The top-level keys are:

| Key | Meaning |
| --- | --- |
| `apiVersion` | required; `vigil.config/v0` |
| `authorities` | required, at least one: `simplex` or `augur`, each with `via` (`file` or `push`, default `file`). A `file` authority requires `path_ref`; a `push` authority forbids it and reads testimony from the in-memory store when `vigil serve` is running (G4). Optional `stale_after` per authority. |
| `listen` | the bind address (section 6) |
| `backends` | named backends, each an `id`, a `kind` (`prom` or `grafana`) and an optional `vantage` slug (section 17); duplicate ids are refused |
| `sources` | informational documents: `scry` or `cubit`, each with a `path_ref` |
| `queries` | named queries (section 7) |
| `freshness` | `stale_after` (default 120s), `skew_budget` (1s to 300s, default 10s) and `declaration_grace_s` (whole seconds, 0 to 86400, default 240); the loader fills each one left out |
| `tokens` | `read_file`, `signal_file` and `testimony_file` (the testimony write token; required in the schema when any authority uses `via: push`) — token file locations (section 6) |

Named backends and named queries come from config only. No config field takes a URL, an address or a selector: a backend's address lives in the off-repo overlay (`vigil.overlay/v0`), keyed by the backend ids declared here. A locator (`path_ref`, a token file) is an environment reference (`env:NAME`) or an absolute path with no traversal. Fixtures for the refusals: `testdata/config/invalid/no-authorities.yaml`, `authority-unknown.yaml`, `query-url.yaml`, `path-ref-traversal.yaml`.

Tests: `internal/config` `TestConfigFixtures`, `TestRefusals` (a duplicate backend id and a duplicate query name among them), `TestListenSpellings`, `TestDefaults`, `TestFreshnessDefaults`, `TestFreshnessRefusals` and `TestBind`, and `FuzzConfig`. In `cmd/vigil`, `TestWildcardListenIsStartupFailure` shows a wildcard listen stops a backend command with exit 5 before any call, and `TestConfigErrorsDoNotEchoValues` and `TestConfigFailureNeverEchoesAHostileField` show the loader's own text, which can quote a value, never reaches stderr.

## 12. Backends and the bounded client

A backend is a named thing Vigil asks questions of: Prometheus or Grafana at V1. Its name and kind come from config; its address comes from the off-repo overlay (section 17). All HTTP to a backend goes through `internal/httpx`, and nothing else in the tree may import `net/http`. There is one client per backend id for the life of the process (`httpx.ForBackend`), so every caller of a backend shares one limiter; the first configuration for an id wins, and a configuration that fails to build is not remembered. The rules live in that package so no caller can loosen them:

- **GET only.** The client has no method parameter and sends no body. The URL is the overlay's base address plus a path fixed in code plus encoded parameters; a caller cannot hand in a URL. A path with a query, a fragment, a dot segment or any character outside the unreserved set is refused.
- **No redirect is followed.** Any 3xx answer is the error `redirect_refused`, and the target of the redirect is never contacted.
- **No proxy.** The transport has no proxy function, so `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are ignored.
- **A deadline on every call.** 5s by default, 10s at most. It covers waiting for a slot, the call and the body read, and passing it is `timeout`.
- **A body cap on every call.** 8 MiB for Prometheus, 16 KiB for Grafana. A body over the cap is `body_cap`, whether it declared its length, streamed without one, or arrived compressed (the cap applies after decompression).
- **Two calls in flight per backend.** A third waits for a slot, inside its own deadline. The limit belongs to the backend id, not to a client value: a command that built a client per call would get a limiter per call, so `ForBackend` hands every caller the same one.
- **Errors carry a code and nothing else.** The detail code is `unreachable`, `timeout`, `body_cap`, `redirect_refused` or `http_status`. The text of the underlying error holds the URL, so it is dropped.

Two guards keep the rule from eroding. depguard rejects `net/http`, subpackages included, in every non-test file outside `internal/httpx` and `internal/server`; `scripts/depguard-selftest.sh` plants each case. `TestNoListenerBeforeV3` parses every non-test Go file except `internal/server` and fails on a listener or a server, so V1 can dial out and cannot listen. V3 relaxes that test on purpose for the serve package only.

The V1 detail codes live in `internal/reasons.Detail`: `unreachable`, `timeout`, `body_cap`, `redirect_refused`, `http_status`, `series_cap`, `query_warnings`, `query_rejected`, `not_health`, `no_series`, `missing_host` and `unexpected_host`. They appear in `sources[].detail_code` and as the `code` of a `vigil.error/v0`. They are never an address, a URL or text copied from a backend.

Tests: `internal/httpx` `TestRedirectsRefused`, `TestProxyEnvironmentIgnored` (every proxy variable set, and a non-loopback logical host, because the standard library never proxies loopback), `TestBodyCap`, `TestBodyCapWithoutContentLength`, `TestBodyCapRejectsDeclaredLengthEarly`, `TestBodyCapStopsReading`, `TestBodyCapAppliesAfterDecompression`, `TestDeadlineOnSlowHeaders`, `TestDeadlineOnSlowBody`, `TestClientDefaultDeadline`, `TestTwoInFlight`, `TestDeadlineCoversWaitingForSlot`, `TestSlotReleasedAfterFailure`, `TestInvalidBase`, `TestInvalidPaths`, `TestErrorsNeverCarryAddresses` and `TestCeilings`. The shared limiter has its own: `TestForBackendSharesOneSemaphorePerID`, `TestForBackendDoesNotShareAcrossIDs`, `TestForBackendFirstConfigWins`, `TestForBackendIsSafeForConcurrentUse`, `TestForBackendDoesNotRememberFailures` and `TestForBackendKeepsTheClientRules`. Each backend package proves it uses the shared limiter with a `TestInFlightBound` of its own (`internal/promclient`, `internal/grafana`, `internal/sources`): three concurrent calls reach the wire two at a time. `FuzzBase` and `FuzzPath` fuzz the address and path rules.

## 13. What `prom targets` and `prom query` may print

An address never reaches stdout. A Prometheus target carries several (`scrapeUrl`, the `instance` label, `lastError` text, discovered labels), so Vigil decodes only what it can publish:

- `prom targets` reads `labels.instance`, `labels.job`, `health` and `lastScrape` and nothing else. The decoder has no field for `scrapeUrl`, `lastError` or `discoveredLabels`, so they cannot be printed by mistake.
- `instance` becomes a slug through the overlay's `hosts[]` (section 17). An instance with no match prints as `unmapped`, even when the query was run with `--host`; the raw value is never shown.
- `job` passes only if it has the shape of a slug. Any other value prints as `redacted`.
- `health` and `lastScrape` pass in their schema shapes: a lowercase health word and an RFC 3339 time.
- `prom query` carries `host` and `job` from the returned labels, by the same two rules, and a sample `value` that must be a number as Prometheus reports it (a decimal, an exponent, `NaN` or an infinity). `vigil.query/v0` bounds that string at 512 characters, above the 327 of the longest float64 written in plain decimal (`TestQueryValueLength`). A label other than those two is dropped.

The schemas back the rules from the other side. `vigil.targets/v0` and `vigil.query/v0` give `host` the slug pattern and `value` the number pattern, so a document that carries an address or a name in those fields does not validate. Fixtures: `testdata/targets/invalid/job-address.json`, `testdata/query/invalid/value-address.json`, `value-name.json` and `value-url.json`.

Tests: `internal/promclient` `TestTargetsMapping` and `TestTargetsMappingRules` plant a `.ts.net` name and an address-shaped instance in the answer and check that neither reaches the document, with the recorded answers `internal/promclient/testdata/prom/targets-mixed.json`, `targets-empty.json` and `targets-not-success.json`. `TestQueryRules` covers the query labels and values. `internal/schematest` `TestV1DocumentDrift`, `TestQueryValueShape` and `TestFixturesValidateAsLabelled` pin the schemas, and `internal/render` `TestTablesPrintOnlyWhatTheDocumentHas` pins that a table never prints a field the document lacks. In `cmd/vigil`, `TestPromTargetsJSON` and `TestPromTargetsTable` check the real stdout for an address, a `scrapeUrl`, a `lastError` and a `discoveredLabels` field.

## 14. Grafana health

`vigil grafana health` calls `GET /api/health` on the named Grafana backend, with a 2s deadline and a 16 KiB cap, and follows no redirect. Healthy means all of: status 200, a JSON body, and `database` equal to `ok`. Healthy prints `vigil.health/v0` and exits 0. Anything else is a failure: exit 4, one `vigil.error/v0` line on stderr whose `code` says why, and empty stdout.

| Answer | Code |
| --- | --- |
| a login page, or any body that is not JSON | `not_health` |
| JSON whose `database` is not `ok` | `not_health` |
| a redirect (a login redirect is the usual one) | `redirect_refused` |
| any other status | `http_status` |
| no answer in 2s | `timeout` |
| connection refused or reset | `unreachable` |
| a body over 16 KiB | `body_cap` |

A login page is not health. Grafana is a source, never an authority. The command fails when Grafana is not healthy, because that is the question it was asked. `vigil sources` asks the same question as part of a listing, so there the unhealthy state is data: the Grafana row says `unavailable` with its detail code, and the command still exits 0. Nothing about Grafana moves `overall`, the chip or the exit code of a summary. Fixture: `grafana-down`.

Tests: `internal/grafana` `TestHealth` (healthy, a database that is down, a login page, an empty body, a server error and a missing path, from `internal/grafana/testdata/grafana/healthy.json`, `db-down.json` and `login-page.txt`) and `TestHealthFailures` (a refused redirect, an oversize body, a slow backend, no address and no backend configured). In `cmd/vigil`, `TestGrafanaHealthHealthy`, `TestGrafanaHealthUnhealthyIsExit4` (every row of the table above, in both `--json` and table form) and `TestGrafanaHealthBackendSelection` (no Grafana backend and two of them are exit 3 before any call).

## 15. The CLI contract

Eight commands exist: `version`, `prom targets`, `prom query`, `grafana health`, `sources`, `validate` and `simplex summary`, with `help` generated by the framework. `validate` loads `--config`, and `--overlay` when given, with the same strict loaders the other commands use, and contacts nothing. The global flags are `--config PATH`, `--overlay PATH`, `--json` and `--now RFC3339`.

- **`--json` prints the document alone.** On success, stdout holds one JSON document and nothing else: `vigil.targets/v0`, `vigil.query/v0`, `vigil.health/v0` (only when healthy), `vigil.sources/v0`, `vigil.validate/v0` or `vigil.summary/v0`. Without it the same document is rendered as a table, and a table never shows a field the document lacks. `version` prints one line.
- **`--now` pins the clock.** It is an RFC 3339 time. It sets the `time` of an instant query, `checked_at`, `evaluated_at` and every age in a table, so two runs against the same answers print the same bytes. Anything else is exit 3.
- **Every failure is one line on stderr.** It is a `vigil.error/v0` document, `{"apiVersion":"vigil.error/v0","error":{"code":"...","message":"..."}}`, followed by a newline, and stdout is empty. This holds for usage errors and a recovered panic too (code `internal`). A command's output is held until it succeeds, so a failure part-way cannot leave half a document behind.
- **The `message` is fixed text.** It is written by Vigil, never copied from an argument, a backend or an error: it holds no URL, no address and no token. The `code` is stable; scripts read the code.
- **No command takes a URL, a selector or PromQL.** A positional argument to a command that takes none, a query name or `--host` of the wrong shape, and a URL in `--config` or `--overlay` are exit 3. Test: `TestURLShapedArgumentsExitUsage`.
- **A verdict is not a failure.** `simplex summary` prints its document and nothing on stderr when it exits 0, 1, 2 or 4. A verdict code never comes without the document: output that cannot be written is exit 6. Tests: `TestSimplexSummaryErrors`, `TestSimplexSummaryUnavailable`, `TestVerdictNeedsItsDocument` and `TestVerdictOf`.
- **Asking for help is a success.** `--help` and `vigil help` print to stdout and exit 0. Running `vigil` or a command group with no subcommand is a usage error.

Tests: `cmd/vigil` `TestUsageErrorsNeverExitAsVerdict`, `TestURLShapedArgumentsExitUsage`, `TestValidArgumentsReachTheHandler`, `TestHandlerErrorsMapToExitAndCode`, `TestUnknownHandlerErrorIsInternalAndSilent`, `TestPanicExitsInternalWithOneJSONLine`, `TestHelpIsASuccess`, `TestHelpAndErrorsDoNotEchoArguments` and `TestCommandSetIsExactlyThePublished`. `validate` is covered by `TestValidateCommand` and `TestValidateContactsNoBackend`. Each validates the stderr line against `schema/vigil.error.v0.schema.json`. `FuzzCLIArgs` feeds arbitrary arguments to the command tree and checks what each handler receives: a success hands over only a query name and a host slug of the right shape and file names that are not URLs, a usage error never runs a handler, and every error line is one of the fixed texts. `TestPromTargetsJSON`, `TestPromQueryJSON`, `TestGrafanaHealthHealthy` and `TestSourcesRowsAreData` validate the real stdout against the document schemas, and run twice with `--now` to show the bytes are the same; `TestNowDefaultsToTheClock` shows the clock is used when `--now` is absent.

## 16. Exit codes for the V1 commands

The table in section 5 holds. Per command, these are the answers:

| Command | 0 | 3 | 4 |
| --- | --- | --- | --- |
| `prom targets` | answered | usage or config | backend unreachable, HTTP error, body cap, timeout |
| `prom query` | answered, including an empty result | usage, config, `missing_host`, `unexpected_host`, `query_rejected` | unreachable, `series_cap`, `query_warnings`, timeout, body cap |
| `grafana health` | healthy; prints the document | usage or config | not healthy or unreachable (`not_health`, `http_status`, `redirect_refused`, `timeout`, `unreachable`); error line, empty stdout |
| `sources` | rendered; an unhealthy or unreachable row is data | usage or config | never |
| `validate` | the files are valid | missing `--config`, invalid config or overlay | never |

`simplex summary` is the command whose exit is a verdict (section 5): 0, 1 or 2 is `overall`, and 4 is a null `overall`, each with the document printed. It exits 3 for usage or config, a missing `simplex` authority included, and 4 with no document when interrupted. It contacts no backend. Tests: `cmd/vigil` `TestSimplexSummaryVerdicts`, `TestSimplexSummaryClock`, `TestSimplexSummaryContactsNoBackend` and `TestSimplexSummaryInterrupted`.

A wildcard `listen` in config is exit 5 for any command that loads the config (section 6). A recovered panic is exit 6. No error path exits 0, 1 or 2: a backend command never produces a verdict.

Tests, by column: exit 0 is `TestPromTargetsJSON`, `TestPromQueryJSON`, `TestGrafanaHealthHealthy` and `TestSourcesRowsAreData`. Exit 3 is `TestPromQueryHostRules` (`missing_host`, `unexpected_host`, `unknown_host`, `unknown_query`), `TestBackendCommandsNeedConfigAndOverlay` and `TestGrafanaHealthBackendSelection`; `query_rejected` is the `bad_data` case in `internal/promclient` `TestQueryRules`. Exit 4 is `TestUnreachableBackendIsExit4`, `TestBackendRedirectIsRefused` and `TestGrafanaHealthUnhealthyIsExit4`. Exit 5 is `TestWildcardListenIsStartupFailure`. Exit 6 is `TestPanicExitsInternalWithOneJSONLine`. That `sources` never exits 4, whatever the backends do, is `TestSourcesRowsAreData`.

## 17. The overlay

`vigil.overlay/v0` is the only place an address exists. It is YAML, strict, and never committed (the scrub fails the tree if one is tracked). It has two keys beyond `apiVersion`:

- `backends[]`: `id` and `address`, keyed by the backend ids declared in config. An address is a scheme, a host, an optional port and optional path segments. Userinfo, a query, a fragment and any `.` or `..` segment are refused. Fixtures: `testdata/overlay/invalid/address-credentials.yaml`, `address-query.yaml` and `address-dot-segment.yaml`.
- `hosts[]` (optional): each `{slug, instances[]}` maps the `instance` labels a backend reports onto one slug. An instance is a host or `host:port` as Prometheus reports it, and it is matched exactly. A host with no instances, or an instance that is a path, is refused. A query's `{{.host}}` becomes the first instance listed for the host. Fixtures: `testdata/overlay/valid/hosts.yaml`, `testdata/overlay/invalid/host-empty-instances.yaml` and `host-instance-path.yaml`.

Config adds one optional key to a backend, `vantage`: the slug of the place the backend is queried from. It is carried in every `prom` result and in the backend's `sources[]` row, so a reader can tell which side of the mesh answered. Fixtures: `testdata/config/valid/vantage.yaml`, `testdata/config/invalid/backend-vantage-not-a-slug.yaml`.

Tests: `internal/config` `TestOverlayFixtures`, `TestOverlayRules` (a duplicate slug and an instance that already maps to another slug among them) and `TestBind`, which refuses an overlay that names a backend the config never declared. `cmd/vigil` `TestOverlayMustMatchTheConfig` shows the same refusal stops a command before any call. Fixtures: `testdata/overlay/valid/minimal.yaml` and `testdata/overlay/valid/hosts.yaml`.

## 18. The sources listing

`vigil sources` lists the configured backends and whether each one answers. It prints `vigil.sources/v0`: one row for each backend in `backends[]`, in config order, and nothing else.

- **A row is the summary's own source shape.** `vigil.sources/v0` references `$defs/source` in the summary schema instead of copying it, so the two cannot drift: `id`, `kind`, `state`, `observed_at`, `last_ok`, an optional `detail_code` and, for a Prometheus backend, `vantage`.
- **How a row is checked.** A Prometheus backend is asked `GET /-/ready`; a Grafana backend is asked the health question of section 14. A healthy answer is `ok` with no `detail_code`. Anything else is `unavailable` with the code that says why: `unreachable`, `timeout`, `body_cap`, `redirect_refused`, `http_status`, `not_health` or `no_address` when the overlay has no address for the backend.
- **A down backend is a row, not a failure.** The command still exits 0, because the question it answers is what is configured and what answers. It never exits 4 (section 16).
- **`last_ok` is null in V1.** Vigil keeps no state between runs, so it cannot say when a backend last answered. `observed_at` is the time of this check, from `--now` when set.
- **Backends only.** The Simplex authority row is part of `vigil.summary/v0` (`simplex summary`), and the informational sources (Scry, Cubit) arrive with V5. This listing invents none: a document with an authority row at this version does not come from this command.
- **Sources move nothing else.** A row's state is data. It does not change `overall`, the chip or any exit code (section 3).

Tests: `internal/sources` `TestSourcesRows` (both healthy, a backend down as a row state, a missing address and no backends as an empty table) and `TestInFlightBound`. Fixtures: `testdata/sources/valid/backends.json`, `testdata/sources/valid/empty.json` and the invalid cases beside them. `cmd/vigil` `TestSourcesRowsAreData` runs four mixes of up and down backends, checks each output against `vigil.sources/v0`, `last_ok`, the vantage and the absence of any address, and `TestSourcesTable` checks the table. Goldens: `testdata/render/sources-backends.txt` and `sources-empty.txt`.

## 19. Fixture index

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

### V1 fixture directories

Each directory holds `valid/` and `invalid/` documents, validated against the schema named in the table by `internal/schematest` (`TestFixturesValidateAsLabelled`), and a mislabelled fixture fails it.

| Directory | Schema | What the invalid cases pin |
| --- | --- | --- |
| `testdata/targets/` | `vigil.targets/v0` | an upper-case health word, an upper-case host, an address in `job` |
| `testdata/query/` | `vigil.query/v0` | a missing `value`, an upper-case name, a numeric `value`, and an address, a name or a URL in `value` |
| `testdata/health/` | `vigil.health/v0` | a spaced `detail_code`, a missing `checked_at`, a verdict word as `state` |
| `testdata/sources/` | `vigil.sources/v0` | a missing `sources`, an unknown `kind`, a verdict word as `state` |
| `testdata/overlay/` | `vigil.overlay/v0` | credentials, a query and a dot segment in an address; a host with no instances; an instance that is a path; an unknown key; a wrong major |
| `testdata/config/` | `vigil.config/v0` | the refusals of section 11, including a vantage that is not a slug and a second template variable |
| `testdata/render/` | none | the table goldens for the four backend documents, pinned by `internal/render` `TestRenderGoldens`, and for the summary, pinned by `TestSummaryGoldens` |

Recorded backend answers are not fixtures of Vigil's documents: they sit beside the code that decodes them, under `internal/promclient/testdata/prom/` and `internal/grafana/testdata/grafana/`, and use slugs and `example.invalid` only.

## 20. Freshness and declaration fields in the summary

Every field here is optional in `vigil.summary/v0`, so a document without them stays valid.

| Field | Where | Meaning |
| --- | --- | --- |
| `received_at` | each verdict and source | when Vigil first saw the document (section 9), or null when it read none |
| `valid_for` | each verdict and source | the producer's own value in seconds as published, not `eff_valid_for`; null when the document has none |
| `declaration` | each verdict and source | the digest the witness document carries, or null when it declares none |
| `declaration` | top level | Vigil's own configured digest, the reference, or null |
| `declaration_drift` | top level | the comparison below |

The reference is Vigil's own `declaration`, never a majority among the witnesses. Only fresh documents are compared: authorities and informational sources alike. A stale or unavailable one says nothing about what is deployed now.

`declaration_drift` has five fields, all required when it is present:

| Field | Meaning |
| --- | --- |
| `reference` | Vigil's digest, or null |
| `in_grace` | true while the grace window is open: `freshness.declaration_grace_s` seconds from the change of Vigil's own declaration |
| `drifted` | true when a fresh, declared document carries a digest other than `reference`, inside the window or after it. With a null `reference` there is nothing to compare, so it is false |
| `undeclared` | the witnesses whose fresh document has a null digest. They are not drift |
| `by_declaration` | one entry per distinct digest among the fresh, declared documents, the reference's own included: `{"declaration": "sha256:...", "subjects": [...]}`, where `subjects` are witness ids and each witness appears once |

Inside the window drift is a listed fact. Once the window is closed, an authority's drift counts: its verdict gains the reason `declaration_drift` and the drift cap of section 1 applies. An informational source's drift is only listed, in `drifted` and `by_declaration`, and colours nothing; so `drifted` true with `in_grace` false does not by itself mean that `overall` was raised. Drift never gives RED, never changes a verdict's severity, and never turns a null `overall` into a colour.

A one-shot read keeps no record of when Vigil's declaration changed, so it has no grace window: `in_grace` is false.

Tests: `internal/schematest` `TestSummaryFreshnessAndDriftFields` and `TestFreshnessDefaultsMatchTheLoader`; `internal/summary` `TestDeclarationDrift`, `TestFreshness` and `TestNoSecondRollup`.

