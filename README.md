# Vigil (Go HTTP ops surface)

Vigil turns the health of a mesh into one document, `vigil.summary/v0`, that a
person, an agent or Facet can read without knowing where anything lives.

What Vigil does:

- Combines the verdicts of named **authorities** (Simplex today, Augur once its
  Vigil fields land) into one `overall` severity. Vigil re-derives nothing: an
  authority owns its rules, Vigil owns the combination.
- Queries named **backends** (Prometheus, Grafana health) through names in its
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

## Reading a summary

The document is a single JSON object with an `apiVersion`. Consumers refuse an
unknown major version; fields are additive within `v0`.

- `overall` is the worst severity across available verdicts, after two caps
  Vigil owns: a stale verdict contributes at least YELLOW, and a missing
  authority caps `overall` at no better than YELLOW (`authority_unavailable`).
  If no authority could be read at all, `overall` is null and a reader must
  render it as unavailable — null is never GREEN.
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
- `sources[]` is where status lives and the only place it lives: backends and
  informational sources each carry `state`, `observed_at`, `last_ok` and a
  machine `detail_code`.
- `augur{}`, `cubit{}` and `scry{}` blocks are data only. They never colour
  `overall` and never move the Facet chip.

Two presentation rules hold everywhere (CLI table, Facet, agents):

- Stale is shown with its age and never as current. Unavailable is shown as
  unavailable. Neither ever shows the last GREEN as current.
- Watchdog-only is GREEN, and a source being down never paints a host.

## Commands

| Command | Since | What it does |
| --- | --- | --- |
| `vigil version` | V0 | version, commit, Go runtime |
| `vigil prom targets` | V1 | scrape targets of the named prom backend: slug, health, last scrape only |
| `vigil prom query <name>` | V1 | run a named query from config; host slug is the only variable |
| `vigil grafana health` | V1 | health of the named grafana backend; never gates `overall` |
| `vigil sources` | V1 | configured backends and their reachability |
| `vigil simplex summary` | V2 | build `vigil.summary/v0`; human table, or the document alone with `--json` |
| `vigil serve` | V3 | loopback HTTP listener for the same document |

Every command: `--json` prints the document alone on stdout; errors are one
JSON line on stderr (`vigil.error/v0` with a stable `code`). `--now` pins the
clock for tests. Details in [docs/cli.md](docs/cli.md).

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

## Security model

- The listener binds `127.0.0.1:10082` by default. A wildcard address is
  rejected in every spelling; a non-loopback bind needs an explicit flag and
  one explicit address. A bracketed IPv6 `listen` value must be quoted in
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
environment reference. Backend addresses live in an overlay
(`example.overlay.yaml` is the tracked example; a real overlay is never
committed). Both examples validate against the JSON Schemas under `schema/`.

A config with exactly one authority must produce `overall` byte-equal to that
authority's own verdict; adding an authority never lowers `overall`. That is
the whole contract — Vigil adds no second opinion of its own.

## Documents

| Document | File | Role |
| --- | --- | --- |
| `vigil.summary/v0` | `schema/vigil.summary.v0.schema.json` | the single produced document |
| `vigil.signal/v0` | `schema/vigil.signal.v0.schema.json` | ephemeral, authenticated POST only |
| `vigil.error/v0` | `schema/vigil.error.v0.schema.json` | every non-200 response |
| `vigil.config/v0` | `schema/vigil.config.v0.schema.json` | YAML config, strict decode |
| `vigil.overlay/v0` | `schema/vigil.overlay.v0.schema.json` | off-repo addresses |

See [docs/cli.md](docs/cli.md) for the command surface and
[docs/semantics.md](docs/semantics.md) for the field meanings and verdict
rules. Contributing: [CONTRIBUTING.md](CONTRIBUTING.md); security policy:
[SECURITY.md](SECURITY.md).

## License

Apache-2.0.
