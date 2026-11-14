# Vigil (Go HTTP ops surface)

Vigil turns the health of a mesh into one document, `vigil.summary/v0`, that a
person, an agent or Facet can read without knowing where anything lives.

What Vigil does:

- Combines the verdicts of named **authorities** (Simplex today, Augur once its
  Vigil fields land) into one `overall` severity. Vigil re-derives nothing: an
  authority owns its rules, Vigil owns the combination.
- Queries named **backends** (Prometheus, Grafana) through names in its
  config. No command or route takes a URL, a selector or PromQL.
- Accepts ephemeral **signals** from authenticated callers. A signal is
  information, never a verdict.

What Vigil does not do: it is not a prober (it never dials a host), it offers
no free-form PromQL, it has no web UI (Grafana stays the deep-metrics UI,
Facet is the chrome), and it never computes severity from raw data.

```
Simplex ──verdict document (file)──────▶            ──vigil.summary/v0 (pull, read token)──▶ Facet
Augur   ──verdict document (file)──────▶  Vigil  ◀──vigil.signal/v0 (POST, write token)─────
prom (lathe) · grafana (lathe) ──named──▶          Cubit / Scry ──▶ informational blocks
```

## Reading a summary (V2)

The summary document is a single JSON object with an `apiVersion`. Consumers
refuse an unknown major version; fields are additive within `v0`.

- `overall` is the worst severity among the readable verdicts in
  `verdicts[]`, after two caps and nothing else. Stale cap: a stale verdict
  counts as at least YELLOW. Missing-authority cap: while any configured
  authority is unavailable, `overall` is at least YELLOW. If no authority is
  readable, `overall` is null. A reader renders null as unavailable — null is
  never GREEN.
- `verdicts[]` says which authority said what: one entry per configured
  authority with its own `state` (`ok`, `stale`, `unavailable`), the severity
  as published, the reasons — Vigil's own codes (`source_stale`,
  `source_unavailable`, `schema_major`, `clock_skew`,
  `authority_unavailable`) plus any authority-level reasons the authority
  publishes, passed through unchanged — and `observed_at`. Simplex publishes
  no authority-level reasons, so today the verdict reasons are Vigil's codes
  only; Simplex's per-host reasons live in `hosts[].reasons`. This is how
  a reader tells "Simplex says" from "Augur says" — read the `source` field,
  never guess from prose.
- `vantage` (`id`, `status` `ok|blind|unchecked`) is carried from Simplex and
  is omitted when Simplex is unavailable. A blind vantage is a verdict
  (`overall` RED, exit 2), not an unavailable source.
- `hosts[]` is the per-host matrix, carried from Simplex only. A clean host
  may carry an empty `reasons[]`.
- `sources[]` is where status lives and the only place it lives: authorities,
  backends and informational sources each carry `state`, `observed_at`,
  `last_ok` and a machine `detail_code`.
- `augur{}`, `cubit{}` and `scry{}` blocks are data only. They never colour
  `overall` and never move the Facet chip.

Two presentation rules hold everywhere (CLI table, Facet, agents):

- Stale is shown with its age and never as current. Unavailable is shown as
  unavailable. Neither ever shows the last GREEN as current.
- Watchdog-only is GREEN, and a source being down never paints a host.

## Commands

| Command | Pack | What it does |
| --- | --- | --- |
| `vigil version` | V0 | version, commit, Go runtime |
| `vigil prom targets` | V1 | scrape targets of the named prom backend |
| `vigil prom query <name>` | V1 | run a named query from config; host slug is the only variable |
| `vigil grafana health` | V1 | health of the named grafana backend; never gates `overall` |
| `vigil sources` | V1 | one row per configured backend with its reachability |
| `vigil serve` | V3 | loopback HTTP listener for the summary document |

## Flags

| Flag | Commands | Meaning |
| --- | --- | --- |
| `--config PATH` | all | path to the vigil.config/v0 file |
| `--overlay PATH` | all | path to the vigil.overlay/v0 file (backend addresses) |
| `--json` | all | print the document alone on stdout |
| `--now RFC 3339` | all | pin the clock; used by tests and deterministic renders |
| `--host SLUG` | `prom query` | host slug for the `{{.host}}` placeholder |

`--help` text is taken from the binary; see [docs/cli.md](docs/cli.md).

## Output contract

Every command renders the same document two ways: the human table (default)
and the document alone with `--json`. `--json` is one line and byte-stable —
the same document always produces the same bytes. A failure prints one
`vigil.error/v0` JSON line on stderr and nothing on stdout. A table never
prints a field the document lacks; a value the document carries as null or
absent renders as a dash.

## Exit codes

Codes 0 to 2 are verdicts: the document was built and its severity is the
code. Codes 3 and up are errors: no verdict was produced. No error path ever
exits 0, 1 or 2. Only that split is shared across the family; this table is
Vigil's own.

| Code | Meaning |
| --- | --- |
| 0 | verdict GREEN |
| 1 | verdict YELLOW (a stale source is at least this) |
| 2 | verdict RED |
| 3 | usage or configuration error |
| 4 | source unavailable or backend unreachable |
| 5 | auth, startup or bind failure |
| 6 | internal error (panic recovered) |

For the V1 backend commands, the mapping per command:

| Command | 0 | 3 | 4 |
| --- | --- | --- | --- |
| `prom targets` | answered | usage or config | unreachable, HTTP error, body cap, timeout |
| `prom query` | answered | usage, config, or a rejected query (`query_rejected`, `missing_host`, `unexpected_host`) | unreachable, `series_cap`, `query_warnings`, timeout, body cap |
| `grafana health` | healthy (`database: ok`) | usage or config | unhealthy or unreachable (`not_health`, `http_status`, timeout) |
| `sources` | rendered — row states are data | usage or config | never |

Two query rules sit on exit 3: the config's `{{.host}}` placeholder needs
`--host` (`missing_host`), and `--host` is refused when the expr has no
placeholder (`unexpected_host`) — neither fans out to the backend. An empty
query result is exit 0 with `series: []`, not an error. A wildcard `listen`
in the config is exit 5 for every command.

## Backend command rules (V1)

- `prom targets` publishes only what may be published: the mapped host slug,
  the job, the health word and the last scrape. An instance the overlay
  cannot map prints as `unmapped`; a job without a publishable shape prints
  as `redacted`. A scrape URL, a last error or discovered labels never reach
  the output.
- `prom query` runs exactly one named query from the config. The host slug is
  the only template variable and `--host` must be a mapped or public slug.
  The request asks for one series more than the cap, so more results fail
  with `series_cap` instead of a truncated answer; any backend warning fails
  with `query_warnings` instead of a partial answer. Every result carries the
  backend and the vantage that answered, and Vigil never fails over silently.
- `grafana health` is healthy only on a 200 whose body says the database is
  ok; a login page or any other non-health body is `not_health`. It is a
  source, never a verdict: when it is unhealthy the command exits 4 with no
  stdout and nothing else moves.
- `sources` renders rows; a down backend is a row state, never a command
  failure.

## Security model

- The listener binds `127.0.0.1:10082` by default. A wildcard address is
  rejected in every spelling; a non-loopback bind needs `--allow-nonloopback`
  and one explicit address. A bracketed IPv6 `listen` value must be quoted in
  YAML: `listen: "[::1]:10082"`.
- A read token (`VIGIL_READ_TOKEN_FILE`) is required on every `/v0/*` route on
  any bind, so a proxy in front of loopback cannot bypass it. A separate
  write token (`VIGIL_SIGNAL_TOKEN_FILE`) guards `POST /v0/signals`. Both fail
  closed: a missing token file, or one with permissions looser than 0600,
  is a startup failure (exit 5), never an open route.
- Secrets are referenced by environment variable or file path, never given on
  the command line. No field accepts a URL; backend addresses live only in an
  off-repo overlay. Plain HTTP over the tailnet; no TLS in v0.

## Configuration

`example.config.yaml` is a valid starting point: slugs only, token paths by
environment reference, and an optional `vantage` label on the prom backend.
Backend addresses live in an overlay (`example.overlay.yaml` is the tracked
example; a real overlay is never committed); the overlay's `hosts[]` maps the
instance labels a backend reports onto public slugs. Both examples validate
against the JSON Schemas under `schema/`.

`overall` is the worst severity among the readable verdicts in `verdicts[]`,
after two caps and nothing else; with one authority and no cap in play,
`overall` is that authority's published severity, and a stale GREEN gives
YELLOW. Adding a configured authority can keep `overall` or make it worse,
never better. Vigil adds no second opinion of its own.

## Documents

| Document | File | Role |
| --- | --- | --- |
| `vigil.summary/v0` | `schema/vigil.summary.v0.schema.json` | the single produced document (V2) |
| `vigil.sources/v0` | `schema/vigil.sources.v0.schema.json` | one row per configured backend |
| `vigil.targets/v0` | `schema/vigil.targets.v0.schema.json` | the prom backend's scrape targets |
| `vigil.query/v0` | `schema/vigil.query.v0.schema.json` | one named query's answer |
| `vigil.health/v0` | `schema/vigil.health.v0.schema.json` | the grafana backend's health |
| `vigil.signal/v0` | `schema/vigil.signal.v0.schema.json` | ephemeral, authenticated POST only (V3) |
| `vigil.error/v0` | `schema/vigil.error.v0.schema.json` | every non-200 response |
| `vigil.config/v0` | `schema/vigil.config.v0.schema.json` | YAML config, strict decode |
| `vigil.overlay/v0` | `schema/vigil.overlay.v0.schema.json` | off-repo addresses and host mapping |

See [docs/cli.md](docs/cli.md) for the command surface and
[docs/semantics.md](docs/semantics.md) for the field meanings and verdict
rules. Contributing: [CONTRIBUTING.md](CONTRIBUTING.md); security policy:
[SECURITY.md](SECURITY.md).

## License

Apache-2.0.
