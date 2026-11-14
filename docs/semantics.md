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

`vigil prom query <name> [--host <slug>]` runs a query from `queries[]` in the config. Free-form PromQL exists nowhere: not on the CLI, not on a route.

- **The name is the only handle.** It must match the shape of a configured name (`^[a-z][a-z0-9_]{0,47}$`). A URL, a selector or an expression in its place is a usage error, exit 3, before any file is read. Test: `TestURLShapedArgumentsExitUsage`.
- **One placeholder, one literal.** The host slug is the only template variable, and the only substitution is the exact text `{{.host}}`. Vigil replaces that text and runs no template engine. Config validation refuses any other `{{` in an `expr`. Fixture: `testdata/config/invalid/expr-second-variable.yaml`.
- **`--host` is checked before it reaches an expression.** It must be a slug (`^[a-z][a-z0-9-]{0,31}$`) that is listed in the overlay's `hosts[]` or is one of the public slugs. Anything else is exit 3. An unchecked value would let a caller write PromQL through a label matcher.
- **Instant queries only.** The request carries `query`, `time` (the pinned clock when `--now` is set), `timeout` and `limit`.
- **Deadline.** 5s by default and 10s at most, set per query in config as `5s` to `10s`. It is sent as the API `timeout` parameter and enforced by a client deadline. A longer value is refused at load. Fixture: `testdata/config/invalid/timeout-too-long.yaml`.
- **Series cap.** `max_series` defaults to 200 and may not exceed 200 (the config refuses a higher value). The request asks for `max_series + 1`, so a result that exceeds the cap is detected and fails as `series_cap`. Fixture: `testdata/config/invalid/max-series-too-high.yaml`.
- **Warnings.** Any `warnings` in a Prometheus answer fails the call as `query_warnings`. Neither a cap nor a warning ever produces a partial answer: the command prints nothing on stdout.
- **A rejected expression.** Prometheus answers `bad_data` for an expression it will not parse. That is a defect in the config, not in the backend, so it is `query_rejected` and exit 3.
- **No silent failover.** Every result carries `backend` and `vantage`. If the configured backend cannot be reached the command exits 4 and says so; it never asks another.
- **Output labels.** Only `host` and `job` are carried from the returned labels, and only in their scrubbed form (section 13).

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

Every document carries an `apiVersion`: `vigil.summary/v0`, `vigil.signal/v0`, `vigil.error/v0`, `vigil.config/v0`, `vigil.overlay/v0`, `vigil.sources/v0`, `vigil.targets/v0`, `vigil.query/v0` and `vigil.health/v0`. Inside `v0` the change rule is additive: a field may be added, and never removed, renamed, re-typed or given a new meaning. Anything else needs a new major. A reader ignores fields it does not know, and refuses a major it does not know.

- Vigil reading an authority document on an unknown major treats that authority as `unavailable`: severity null, reason `authority_unavailable`, `detail_code` `schema_major`. The caps in section 1 then apply. Fixture: `schema-major-mismatch`.
- A consumer of `vigil.summary` (Facet, an agent) refuses a summary on an unknown major instead of guessing at its fields.
- The nine Vigil schemas under `schema/` are the contract: `vigil.summary`, `vigil.signal`, `vigil.error`, `vigil.config`, `vigil.overlay`, `vigil.sources`, `vigil.targets`, `vigil.query` and `vigil.health`, each at `v0`. `simplex.probe.v0.schema.json` beside them is a vendored copy of Simplex's own contract, not a Vigil document. A conformance test validates every fixture against them, and the binary never runs the schema validator.

## 11. Configuration

`vigil.config/v0` is YAML and strict. The schema `schema/vigil.config.v0.schema.json` is the key list: an unknown key at any level is refused, and so is an unknown `apiVersion` major. Both are configuration errors, exit 3. Fixtures: `testdata/config/invalid/unknown-key.yaml` and `testdata/config/invalid/wrong-major.yaml`.

The top-level keys are:

| Key | Meaning |
| --- | --- |
| `apiVersion` | required; `vigil.config/v0` |
| `authorities` | required, at least one: `simplex` or `augur`, each with a `path_ref` and an optional `stale_after` |
| `listen` | the bind address (section 6) |
| `backends` | named backends, each an `id`, a `kind` (`prom` or `grafana`) and an optional `vantage` slug (section 17); duplicate ids are refused |
| `sources` | informational documents: `scry` or `cubit`, each with a `path_ref` |
| `queries` | named queries (section 7) |
| `freshness` | `stale_after`, default 120s |
| `tokens` | `read_file` and `signal_file`, the token file locations (section 6) |

Named backends and named queries come from config only. No config field takes a URL, an address or a selector: a backend's address lives in the off-repo overlay (`vigil.overlay/v0`), keyed by the backend ids declared here. A locator (`path_ref`, a token file) is an environment reference (`env:NAME`) or an absolute path with no traversal. Fixtures for the refusals: `testdata/config/invalid/no-authorities.yaml`, `authority-unknown.yaml`, `query-url.yaml`, `path-ref-traversal.yaml`.

## 12. Backends and the bounded client

A backend is a named thing Vigil asks questions of: Prometheus or Grafana at V1. Its name and kind come from config; its address comes from the off-repo overlay (section 17). All HTTP to a backend goes through `internal/httpx`, one client per backend, and nothing else in the tree may import `net/http`. The rules live in that package so no caller can loosen them:

- **GET only.** The client has no method parameter and sends no body. The URL is the overlay's base address plus a path fixed in code plus encoded parameters; a caller cannot hand in a URL. A path with a query, a fragment, a dot segment or any character outside the unreserved set is refused.
- **No redirect is followed.** Any 3xx answer is the error `redirect_refused`, and the target of the redirect is never contacted.
- **No proxy.** The transport has no proxy function, so `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are ignored.
- **A deadline on every call.** 5s by default, 10s at most. It covers waiting for a slot, the call and the body read, and passing it is `timeout`.
- **A body cap on every call.** 8 MiB for Prometheus, 16 KiB for Grafana. A body over the cap is `body_cap`, whether it declared its length, streamed without one, or arrived compressed (the cap applies after decompression).
- **Two calls in flight per backend.** A third waits for a slot, inside its own deadline.
- **Errors carry a code and nothing else.** The detail code is `unreachable`, `timeout`, `body_cap`, `redirect_refused` or `http_status`. The text of the underlying error holds the URL, so it is dropped.

Tests: `internal/httpx` (`TestRedirectsRefused`, `TestProxyEnvironmentIgnored` with every proxy variable set, `TestBodyCap`, `TestBodyCapWithoutContentLength`, `TestBodyCapStopsReading`, `TestBodyCapAppliesAfterDecompression`, `TestDeadlineOnSlowHeaders`, `TestDeadlineOnSlowBody`, `TestTwoInFlight`, `TestDeadlineCoversWaitingForSlot`, `TestInvalidPaths`, `TestErrorsNeverCarryAddresses`). The proxy test reaches a non-loopback name through a dial hook, because the standard library never proxies loopback and a test against loopback would pass for the wrong reason.

Two guards keep the rule from eroding. depguard rejects `net/http`, subpackages included, in every non-test file outside `internal/httpx`; `scripts/depguard-selftest.sh` plants each case. `TestNoListenerBeforeV3` parses every non-test Go file and fails on a listener or a server, so V1 can dial out and cannot listen. V3 relaxes that test on purpose.

The V1 detail codes live in `internal/reasons.Detail`: `unreachable`, `timeout`, `body_cap`, `redirect_refused`, `http_status`, `series_cap`, `query_warnings`, `query_rejected`, `not_health` and `no_series`. They appear in `sources[].detail_code` and as the `code` of a `vigil.error/v0`. They are never an address, a URL or text copied from a backend.

## 13. What `prom targets` and `prom query` may print

An address never reaches stdout. A Prometheus target carries several (`scrapeUrl`, the `instance` label, `lastError` text, discovered labels), so Vigil decodes only what it can publish:

- `prom targets` reads `labels.instance`, `labels.job`, `health` and `lastScrape` and nothing else. The decoder has no field for `scrapeUrl`, `lastError` or `discoveredLabels`, so they cannot be printed by mistake.
- `instance` becomes a slug through the overlay's `hosts[]` (section 17). An instance with no match prints as `unmapped`; the raw value is never shown.
- `job` passes only if it has the shape of a slug. Any other value prints as `redacted`.
- `health` and `lastScrape` pass in their schema shapes: a lowercase health word and an RFC 3339 time.
- `prom query` carries `host` and `job` from the returned labels, by the same two rules, and a sample `value` that must be a number as Prometheus reports it (a decimal, an exponent, `NaN` or an infinity). A label other than those two is dropped.

The schemas back the rules from the other side. `vigil.targets/v0` and `vigil.query/v0` give `host` the slug pattern and `value` the number pattern, so a document that carries an address or a name in those fields does not validate. Fixtures: `testdata/targets/invalid/job-address.json`, `testdata/query/invalid/value-address.json`, `value-name.json` and `value-url.json`.

## 14. Grafana health

`vigil grafana health` calls `GET /api/health` on the named Grafana backend, with a 2s deadline and a 16 KiB cap, and follows no redirect. Healthy means all of: status 200, a JSON body, and `database` equal to `ok`. Everything else is not healthy, and says why:

| Answer | Detail code |
| --- | --- |
| a login page, or any body that is not JSON | `not_health` |
| JSON whose `database` is not `ok` | `not_health` |
| a redirect (a login redirect is the usual one) | `redirect_refused` |
| any other status | `http_status` |
| no answer in 2s | `timeout` |
| connection refused or reset | `unreachable` |
| a body over 16 KiB | `body_cap` |

A login page is not health. Grafana is a source, never an authority: its state appears in `sources[]` and moves nothing else, not `overall`, not the chip and not the exit code of a summary. Fixture: `grafana-down`.

## 15. The CLI contract

Six commands exist: `version`, `prom targets`, `prom query`, `grafana health` and `sources`, with `help` generated by the framework. The global flags are `--config PATH`, `--overlay PATH`, `--json` and `--now RFC3339`.

- **`--json` prints the document alone.** On success, stdout holds one JSON document and nothing else: `vigil.targets/v0`, `vigil.query/v0`, `vigil.health/v0` or `vigil.sources/v0`. Without it the same document is rendered as a table, and a table never shows a field the document lacks. `version` prints one line.
- **`--now` pins the clock.** It is an RFC 3339 time. It sets the `time` of an instant query, `checked_at`, `evaluated_at` and every age in a table, so two runs against the same answers print the same bytes. Anything else is exit 3.
- **Every failure is one line on stderr.** It is a `vigil.error/v0` document, `{"apiVersion":"vigil.error/v0","error":{"code":"...","message":"..."}}`, followed by a newline, and stdout is empty. This holds for usage errors and a recovered panic too (code `internal`). A command's output is held until it succeeds, so a failure part-way cannot leave half a document behind.
- **The `message` is fixed text.** It is written by Vigil, never copied from an argument, a backend or an error: it holds no URL, no address and no token. The `code` is stable; scripts read the code.
- **No command takes a URL, a selector or PromQL.** A positional argument to a command that takes none, a query name or `--host` of the wrong shape, and a URL in `--config` or `--overlay` are exit 3. Test: `TestURLShapedArgumentsExitUsage`.
- **Asking for help is a success.** `--help` and `vigil help` print to stdout and exit 0. Running `vigil` or a command group with no subcommand is a usage error.

Tests: `cmd/vigil` (`TestUsageErrorsNeverExitAsVerdict`, `TestHandlerErrorsMapToExitAndCode`, `TestUnknownHandlerErrorIsInternalAndSilent`, `TestPanicExitsInternalWithOneJSONLine`, `TestHelpIsASuccess`, `TestCommandSetIsExactlyTheSix`). Each validates the stderr line against `schema/vigil.error.v0.schema.json`.

## 16. Exit codes for the V1 commands

The table in section 5 holds. Per command, these are the answers:

| Command | 0 | 3 | 4 |
| --- | --- | --- | --- |
| `prom targets` | answered | usage or config | backend unreachable, HTTP error, body cap, timeout |
| `prom query` | answered | usage, config, `query_rejected` | unreachable, `series_cap`, `query_warnings`, timeout, body cap |
| `grafana health` | healthy | usage or config | not healthy or unreachable (`not_health`, `http_status`, `redirect_refused`, `timeout`, `unreachable`) |
| `sources` | rendered; a row's state is data | usage or config | never |

A wildcard `listen` in config is exit 5 for any command that loads the config (section 6). A recovered panic is exit 6. No error path exits 0, 1 or 2: a backend command never produces a verdict.

## 17. The overlay

`vigil.overlay/v0` is the only place an address exists. It is YAML, strict, and never committed (the scrub fails the tree if one is tracked). It has two keys beyond `apiVersion`:

- `backends[]`: `id` and `address`, keyed by the backend ids declared in config. An address is a scheme, a host, an optional port and optional path segments. Userinfo, a query, a fragment and any `.` or `..` segment are refused. Fixtures: `testdata/overlay/invalid/address-credentials.yaml`, `address-query.yaml` and `address-dot-segment.yaml`.
- `hosts[]` (optional): each `{slug, instances[]}` maps the `instance` labels a backend reports onto one public slug. An instance is a host or `host:port` as Prometheus reports it, and it is matched exactly. A host with no instances, or an instance that is a path, is refused. Fixtures: `testdata/overlay/valid/hosts.yaml`, `testdata/overlay/invalid/host-empty-instances.yaml` and `host-instance-path.yaml`.

Config adds one optional key to a backend, `vantage`: the slug of the place the backend is queried from. It is carried in every `prom` result and in the backend's `sources[]` row, so a reader can tell which side of the mesh answered. Fixtures: `testdata/config/valid/vantage.yaml`, `testdata/config/invalid/backend-vantage-not-a-slug.yaml`.

## 18. Fixture index

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
