# Vigil CLI (V2)

One command surface, one document per command. `--json` prints the document
alone on stdout; every failure prints one JSON line on stderr and nothing on
stdout. The human tables are renderers of the same document — no command
invents a second shape, and a table never prints a field the document lacks.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--config PATH` | path to the vigil.config/v0 file |
| `--overlay PATH` | path to the vigil.overlay/v0 file (backend addresses) |
| `--json` | print the document alone on stdout (no table, no decoration) |
| `--now <RFC 3339>` | pin the clock; used by tests and deterministic renders |

`vigil --help` output, captured from the binary:

```
Vigil reads named backends (Prometheus, Grafana) and authority documents
through names in its config. No command takes a URL, a selector or PromQL.
Usage:
  vigil [flags]
  vigil [command]
Available Commands:
  grafana     Check the named Grafana backend
  help        Help about any command
  prom        Query the named Prometheus backend
  simplex     Combine what Simplex published
  serve       Listen for vigil.summary/v0 over HTTP
  sources     List the configured backends and their reachability
  validate    Validate config and optional overlay without probing
  version     Print the version, the commit and the Go runtime
Flags:
      --config string    path to the vigil.config/v0 file
  -h, --help             help for vigil
      --json             print the document alone on stdout
      --now string       pin the clock to an RFC 3339 time (tests and deterministic renders)
      --overlay string   path to the vigil.overlay/v0 file (backend addresses)
Use "vigil [command] --help" for more information about a command.
```

Every subcommand accepts the four global flags as global flags and `-h,
--help`; `prom query` adds `--host`. Use `vigil <command> --help` for each
command's own text.

A path value must be a path: a URL-shaped `--config` or `--overlay` value is
a usage error (exit 3). A malformed `--now` is also exit 3.

## Errors

Every failure — usage errors included — is one `vigil.error/v0` JSON line on
stderr:

```json
{"apiVersion":"vigil.error/v0","error":{"code":"backend_unreachable"}}
```

`code` is a stable machine code; `message` is optional human text and never
carries a URL, a token or anything that distinguishes a missing credential
from a wrong one. Codes on the V1 surface include `unreachable`, `timeout`,
`body_cap`, `redirect_refused`, `http_status`, `series_cap`,
`query_warnings`, `query_rejected`, `not_health`, `missing_host`,
`unexpected_host`, `missing_config`, `missing_authority`, `interrupted` and
`usage`. Stdout stays empty on every failure. A verdict is not a failure:
`vigil simplex summary` prints its document when it exits 1, 2 or 4.

## vigil version (V0)

Prints the binary version, the commit and the Go runtime.

```
$ vigil version
vigil v0-dev (commit 1a2b3c4d5e6f, go1.27.4)
```

## vigil prom targets (V1)

Lists the active scrape targets of the named prom backend. Table columns, in
order:

| Column | Content |
| --- | --- |
| `TARGET` | scrape slug: the overlay-mapped instance |
| `JOB` | the job label when it has a publishable shape, `redacted` otherwise |
| `HEALTH` | the backend's own health word: `up`, `down` or `unknown` |
| `LAST_SCRAPE` | when the backend last scraped it; `-` when it never did |

The header block above the table carries `BACKEND`, `VANTAGE` and
`GENERATED_AT` from the document. An instance the overlay's `hosts[]` cannot
map prints as `unmapped`. The decoder never reads a scrape URL, a last error
or discovered labels, so an address in those fields cannot reach the output
even by accident.

## vigil prom query <name> (V1)

Runs one named query from `vigil.config/v0`. The name is the only handle:
there is no free-form path over HTTP or on the CLI, and `{{.host}}` is the
only template variable. `--host` takes a slug; the expr receives the first
entry of that host's `instances` list in the overlay, so list the label the
backend holds first. The label is inserted as written: inside a `=~` matcher
its dots and brackets keep their regular-expression meaning.

| Flag | Meaning |
| --- | --- |
| `--host SLUG` | a slug the overlay's `hosts[]` defines; `{{.host}}` becomes that host's first instance label |

Placeholder rules, both exit 3 with no backend call:

- the expr has `{{.host}}` and `--host` is absent → `missing_host`;
- `--host` is given and the expr has no placeholder → `unexpected_host`.

Bounds the config enforces, so the docs can promise them:

- instant queries only; a per-query deadline (default 5s, at most 10s) is
  sent to the backend and enforced locally;
- a series cap (default 200; the request asks for one more than the cap) —
  more results fail with `series_cap`, never a truncated answer;
- any warning the backend reports fails with `query_warnings` — never a
  partial answer presented as whole;
- a query the backend rejects (`bad_data` on a configured expr) is
  `query_rejected` and exits 3.

An empty result is not an error: exit 0 with `series: []`.

Table columns, in order:

| Column | Content |
| --- | --- |
| `HOST` | the series' mapped host slug |
| `JOB` | the publishable job label; the column exists only when some series carries one |
| `VALUE` | the sample exactly as Prometheus reports it: a decimal, an exponent, `NaN` or an infinity |

The header block carries `BACKEND`, `VANTAGE`, `NAME`, `HOST` (only when the
query was run for one host) and `EVALUATED_AT`. Every result is answered by
the backend and vantage the config named; Vigil never fails over silently —
if the configured backend cannot be reached the command exits 4 and says so.

## vigil grafana health (V1)

Calls `GET /api/health` on the named grafana backend with a 2s timeout, a
16 KiB body cap and no redirects. Healthy means a 200 whose body reports the
database ok; a login page or any other non-health body is `not_health`, and
a non-200 is `http_status`.

The document is printed only when the backend is healthy: an unhealthy check
is exit 4 with one error line on stderr and empty stdout. Grafana is a
source, never an authority — when it is down, nothing else moves: not
`overall`, not the chip, not any other command.

Output (a key-value block; the `DETAIL_CODE` line appears only when the
document carries the field):

```
BACKEND     grafana
STATE       ok
CHECKED_AT  2026-11-14T09:00:00Z
```

## vigil sources (V1)

Lists one row per configured backend with its reachability. Table columns,
in order:

| Column | Content |
| --- | --- |
| `ID` | the backend id from the config |
| `KIND` | `prom` or `grafana` |
| `STATE` | `ok`, `stale` or `unavailable` |
| `OBSERVED_AT` | when the backend was last checked; `-` when never |
| `LAST_OK` | the last time the check succeeded; `-` when it never has |
| `DETAIL_CODE` | why a row is down; the column exists only when some row carries one |
| `VANTAGE` | the prom vantage that answered; the column exists only when some row carries one |

A prom row is checked through `/-/ready` and carries its vantage; a grafana
row uses the health check. Rows are data: a down backend is a row state, and
the command still exits 0. `last_ok` stays null — no state is kept between
runs. This command lists backends only: the authority row is in
`vigil simplex summary`, and informational rows arrive at V5.

## vigil simplex summary (V2)

Reads the document Simplex publishes at the `simplex` authority's `path_ref`
and prints one `vigil.summary/v0`. It reads files only: no backend is
contacted, no program is run and nothing listens.

```
vigil simplex summary --config FILE [--overlay FILE] [--json] [--now TIME]
```

| Flag | Meaning |
| --- | --- |
| `--config PATH` | required; it must declare a `simplex` authority, or the command exits 3 (`missing_authority`) |
| `--overlay PATH` | optional; checked against the config when given, and never used to contact anything |
| `--json` | the document alone, one line |
| `--now <RFC 3339>` | pin the clock; `generated_at`, every age and freshness come from it |

What it evaluates in V2:

- Every configured authority appears in `verdicts[]` and `sources[]`, in
  config order. Only Simplex has a reader yet: until Augur's ships, a
  configured `augur` authority is reported `unavailable`
  (`source_unavailable`), its `path_ref` is not opened, and `overall` is at
  least YELLOW. List only `simplex` under `authorities` in a V2 config.
  Backends and informational sources add no rows.
- The document is fresh within the authority's `stale_after` (else
  `freshness.stale_after`) and its own `valid_for`; `freshness.skew_budget`
  bounds the clock difference. `received_at` is the file's modification time,
  never later than now.
- Declaration drift compares the document with the config's `declaration`. A
  one-shot run keeps no record of when that changed, so there is no grace
  window: `in_grace` is false and `declaration_grace_s` has no effect here.

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | `overall` GREEN |
| 1 | `overall` YELLOW; a stale or drifted document, or an unavailable authority beside a readable one, is at least this |
| 2 | `overall` RED, a blind vantage included |
| 3 | usage or configuration error |
| 4 | `overall` null: no authority could be read; the summary is still printed |
| 5 | a wildcard `listen` in the config |
| 6 | internal error, or the output could not be written |

With 0, 1, 2 and 4 the document is on stdout and stderr is empty. Otherwise
there is no document, only the error line. An interrupted run also exits 4,
with the error code `interrupted` and no document.

When the document is unusable, its `sources[]` row says why in `detail_code`:
`source_unavailable` or `schema_major`. The path and the underlying error are
never printed.

The table has a header block (`OVERALL`, which reads `unavailable` when null,
`GENERATED_AT`, `VANTAGE`, `VANTAGE_STATUS`, `DECLARATION`), then verdicts,
hosts, sources and the declaration comparison. A verdict row shows its age.
When Simplex is stale, every host row says so and shows the age too.

## vigil validate

Validates a `vigil.config/v0` file (`--config`) and an optional
`vigil.overlay/v0` file (`--overlay`) using the strict loader without
contacting any backend. Config validation admits optional top-level
`declaration` (`^sha256:[0-9a-f]{64}$` or `null`).

Exits `0` when valid (`ok` in human output, or one `vigil.validate/v0` line
with `--json`:
`{"apiVersion":"vigil.validate/v0","status":"ok","config":true,"overlay":<bool>}`),
`3` on a missing `--config` or on config or overlay errors, and `5` on a
wildcard `listen` address.

## vigil serve (V3)

```
vigil serve --config FILE [--overlay FILE] [--allow-nonloopback] [--now TIME]
```

| Flag | Meaning |
| --- | --- |
| `--config PATH` | required; supplies `listen`, authorities, freshness and token locators |
| `--overlay PATH` | optional; checked against the config when given, and never used to dial a backend in V3 G2 |
| `--allow-nonloopback` | permit a `listen` address that is not loopback |
| `--now <RFC 3339>` | pin the clock for every summary the listener returns |

Starts the HTTP listener on the configured `listen` address (default
`127.0.0.1:10082`). A missing read token file, a token file that others can
read, an empty token, a wildcard `listen`, a non-loopback `listen` without
`--allow-nonloopback`, or a bind failure exits **5** before any route opens,
with one `vigil.error/v0` line on stderr and nothing on stdout.

Routes shipped in V3 G2:

| Route | Method | Auth |
| --- | --- | --- |
| `/healthz` | GET | open; returns `{"status":"ok"}` only |
| `/v0/summary` | GET | read token (`Authorization: Bearer`) |
| `/v0/hosts`, `/v0/hosts/{id}` | GET | read token |
| `/v0/sources` | GET | read token (the authority rows of `vigil.summary/v0`, not a live backend probe) |

`GET /v0/summary` returns the same bytes as `vigil simplex summary --json` for
the same config, overlay check, and clock. `GET /v0/hosts` returns the
`hosts[]` slice from that summary; `GET /v0/hosts/{id}` returns one host or
404; `GET /v0/sources` returns the summary `sources[]` slice only.

Signals and testimony routes arrive in later V3 gates (`/v0/signals`,
`POST /v0/testimony/{source}`): until then they answer 404 or 405. The read
token is required on every `/v0/*` route on any bind. Missing and wrong
credentials get byte-identical 401 bodies with no `WWW-Authenticate` header:

```json
{"apiVersion":"vigil.error/v0","error":{"code":"unauthorized"}}
```

Every other non-200 is a `vigil.error/v0` document (`not_found`,
`method_not_allowed`, `internal`). Unknown paths are 404; the wrong method on
a known path is 405. JSON only.

SIGINT and SIGTERM stop the listener gracefully; the command exits **0**.

Planned routes (not yet in G2):

| Route | Method | Auth |
| --- | --- | --- |
| `/v0/signals` | GET, POST | read token for GET; write token for POST |

A wildcard `listen` is rejected in every spelling. A non-loopback bind needs
`--allow-nonloopback` and one explicit address in the config. Quote a
bracketed IPv6 value in YAML: `listen: "[::1]:10082"`.

Signal and testimony limits are stated in [docs/semantics.md](semantics.md).

## Configuration

Commands read `vigil.config/v0` (strict: unknown keys are errors). See
`example.config.yaml` for a valid starting point — it shows the optional
`vantage` label on the prom backend — and `example.overlay.yaml` for where
addresses and the `hosts[]` instance mapping live. An overlay is never
committed; the tracked example uses only documentation placeholders. Token
files are referenced by environment variable or path, never a value.
