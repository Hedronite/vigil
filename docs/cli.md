# Vigil CLI (V1)

One command surface, one document per command. `--json` prints the document
alone on stdout; every failure prints one JSON line on stderr and nothing on
stdout. The human tables are renderers of the same document — no command
invents a second shape, and a table never prints a field the document lacks.

The `--help` texts below are drafted from the command tree; they are
refreshed from the real binary once the command wiring merges.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--config PATH` | path to the vigil.config/v0 file |
| `--overlay PATH` | path to the vigil.overlay/v0 file (backend addresses) |
| `--json` | print the document alone on stdout (no table, no decoration) |
| `--now <RFC 3339>` | pin the clock; used by tests and deterministic renders |

`--help` output (draft, from the binary's command tree):

```
Vigil reads named backends (Prometheus, Grafana) through names in its
config. No command takes a URL, a selector or PromQL.

Flags:
  --config string   path to the vigil.config/v0 file
  --overlay string  path to the vigil.overlay/v0 file (backend addresses)
  --json            print the document alone on stdout
  --now string      pin the clock to an RFC 3339 time (tests and deterministic renders)

Commands:
  prom      Query the named Prometheus backend
  grafana   Check the named Grafana backend
  sources   List the configured backends and their reachability
  version   Print the version, the commit and the Go runtime
```

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
`unexpected_host` and `usage`. Stdout stays empty on every failure.

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
there is no free-form path over HTTP or on the CLI, and the host slug
(`{{.host}}`) is the only template variable.

| Flag | Meaning |
| --- | --- |
| `--host SLUG` | fills `{{.host}}`; must be a mapped or public slug |

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
the command still exits 0. `last_ok` stays null in V1 — no state is kept
between runs. Authority rows arrive at V2 and informational rows at V5.

## vigil serve (V3)

Starts the loopback listener. Routes:

| Route | Method | Auth |
| --- | --- | --- |
| `/healthz` | GET | open; returns `{"status":"ok"}` only |
| `/v0/summary` | GET | read token |
| `/v0/hosts`, `/v0/hosts/{id}` | GET | read token |
| `/v0/sources` | GET | read token |
| `/v0/signals` | GET, POST | read token for GET; write token for POST |

The read token is required on every `/v0/*` route on any bind, so a proxy in
front of loopback cannot bypass it. Both token files fail closed at startup:
a missing file, or one with permissions looser than 0600, makes `vigil serve`
exit 5 before any route opens. Missing and wrong credentials get
byte-identical 401 bodies with no hint of which failed. `POST /v0/signals`
takes a `vigil.signal/v0` body: `source`, `kind`, `severity` (`info` or
`notice`), optional `note` (at most 280 bytes, no control characters) and
`ttl` (clamped into 60s–1h; absent means 5m). `id` and `created_at` are
assigned by the server and must be absent from a request. Signals live in a
256-entry in-memory ring, are not persisted, and never change `overall` or
`sources[]`.

The listener binds `127.0.0.1:10082` by default. A wildcard listen is
rejected in every spelling and exits 5, and the error names the offending
address whether it came from the config or a flag. A non-loopback bind needs
`--allow-nonloopback` plus one explicit address in the config or on the
command line. A bracketed IPv6 value must be quoted in YAML:
`listen: "[::1]:10082"`.

The remaining limits — the signal body cap and per-remote rate, the response
size cap, the per-backend concurrency, and the no-redirect rule — are stated
once in [docs/semantics.md](docs/semantics.md); this doc does not restate
them.

## Configuration

Commands read `vigil.config/v0` (strict: unknown keys are errors). See
`example.config.yaml` for a valid starting point — it shows the optional
`vantage` label on the prom backend — and `example.overlay.yaml` for where
addresses and the `hosts[]` instance mapping live. An overlay is never
committed; the tracked example uses only documentation placeholders. Token
files are referenced by environment variable or path, never a value.
