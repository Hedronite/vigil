# Vigil CLI (v0)

One command surface, one document. `--json` prints the document alone on
stdout; errors are one JSON line on stderr. The human tables are renderers of
the same document — no command invents a second shape.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--json` | print the document alone on stdout (no table, no decoration) |
| `--now <RFC 3339>` | pin the clock; used by tests and deterministic renders |

Errors from any command are one JSON line on stderr:

```json
{"apiVersion":"vigil.error/v0","error":{"code":"backend_unreachable"}}
```

`code` is a stable machine code; `message` is optional human text and never
carries a URL, a token or anything that distinguishes a missing credential
from a wrong one. Codes seen on the wire include `unauthorized`,
`not_found`, `method_not_allowed`, `payload_too_large`, `query_warnings`,
`series_cap` and `backend_unreachable`. Startup and configuration failures
print a plain line on stderr and exit 5 or 3; see the exit table in the
README.

## vigil version (V0)

Prints the binary version, the commit and the Go runtime. Available now.

```
$ vigil version
vigil v0-dev (commit 8b90176cc6dc, go1.27.3)
```

## vigil prom targets (V1)

Lists the active scrape targets of the named prom backend. Columns, in order:

| Column | Content |
| --- | --- |
| `TARGET` | scrape slug (the configured slug for the instance) |
| `HEALTH` | the backend's own health for the target |
| `LAST SCRAPE` | when the backend last scraped it |

An instance Vigil cannot map to a slug prints as `unmapped`. The output never
includes a scrape URL, a last error or discovered labels: addresses do not
belong in CLI output.

## vigil prom query (V1)

Runs one named query from `vigil.config/v0`. There is no free-form path over
HTTP or on the CLI — the name is the only handle, and the host slug
(`{{.host}}`) is the only template variable.

Bounds the config enforces, so the docs can promise them:

- instant queries only; a per-query deadline (default 5s, at most 10s) is
  sent to the backend and enforced locally;
- a series cap (default 200; the request asks for one more than the cap) —
  more results fail with `series_cap`, never a truncated answer;
- any warning the backend reports fails with `query_warnings` — never a
  partial answer presented as whole.

Every result carries the `backend` and the `vantage` that answered. Vigil
never fails over silently: if the configured backend cannot be reached the
command exits 4 and says so.

## vigil grafana health (V1)

Calls `GET /api/health` on the named grafana backend. A login page is not
health. Grafana is a source, never an authority: when it is down,
`sources[]` shows it down and nothing else moves — not `overall`, not the
chip, not the exit code of a summary.

## vigil sources (V1)

Lists the configured backends and authorities with their reachability and the
last time each was read. Same columns as the `sources[]` table below.

## vigil simplex summary (V2)

Builds `vigil.summary/v0` from the configured authorities and renders it.
Default output is the human table; `--json` prints the document alone. The
bytes `--json` prints are the same bytes `vigil serve` serves — one document,
two renderers, test-pinned.

Table columns, in order:

| Table | Columns |
| --- | --- |
| header | `OVERALL`, `AGE` (of `generated_at`) |
| verdicts | `SOURCE`, `STATE`, `SEVERITY`, `AGE`, `REASONS` |
| hosts | `HOST`, `SEVERITY`, `REASONS` |
| sources | `SOURCE`, `KIND`, `STATE`, `AGE`, `DETAIL` |
| signals | `KIND`, `SEVERITY`, `AGE`, `NOTE` |

Reading the table:

- The `SOURCE` column is the authority's name (`simplex`, later `augur`);
  that is how a reader tells "Simplex says" from "Augur says". A verdict whose
  document could not be read still appears, as `unavailable` with no
  severity.
- `STATE` is `ok`, `stale` or `unavailable`. A stale row shows its age and is
  never presented as current; an unavailable row shows `unavailable`. Neither
  ever shows the last GREEN as current.
- `SEVERITY` in the verdicts table is the severity as published by the
  authority; `overall` carries the capped combination. When they differ, the
  age column and the `REASONS` column (`source_stale`,
  `authority_unavailable`, `schema_major`, `clock_skew`,
  `source_unavailable`) say why.
- `hosts[]` comes from Simplex alone; a clean host may have an empty reasons
  cell.
- Signals render as informational lines only. Their severity vocabulary is
  `info` and `notice` — deliberately not GREEN/YELLOW/RED — and they never
  touch `overall`, the chip or the exit code.

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

## Configuration

Commands read `vigil.config/v0` (strict: unknown keys are errors). See
`example.config.yaml` for a valid starting point and `example.overlay.yaml`
for where addresses live — an overlay is never committed; the tracked example
uses only documentation placeholders. Token files are referenced by
environment variable or path, never a value.
