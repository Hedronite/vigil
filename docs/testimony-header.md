# Testimony header (v0)

Normative specification for the shared testimony header emitted by the four witness producers (`simplex`, `augur`, `scry`, `cubit`) and consumed by `vigil`.

## 1. Purpose

Vigil combines verdicts and informational documents without probing hosts directly or re-deriving witness rules. That separation relies on one rule: **a tool must say what it could not see**.

Every witness document carries four shared header fields so a consumer can tell:

- when the producer finished observing (`generated_at`),
- whether any first-hand observation actually occurred (`observed`),
- how long the producer stands behind the document (`valid_for`), and
- which watch declaration configured the producer (`declaration`).

See [semantics.md](semantics.md) for Vigil's overall combination rules and [cli.md](cli.md) for the command surface.

## 2. Normative field table

| Field | Type | Required schema constraint | Semantics |
| --- | --- | --- | --- |
| `generated_at` | `string` | RFC 3339 UTC timestamp (`Z` suffix) | When the producer finished observing (existing field, unchanged). |
| `observed` | `boolean` | `true` or `false` | `false` means the document carries no first-hand observation and carries no verdict colour. |
| `valid_for` | `integer` | Integer seconds `>= 1` | How long the producer stands behind the document. Never a duration string or float. |
| `declaration` | `string \| null` | `^sha256:[0-9a-f]{64}$` or `null` | Copied verbatim from optional config key `declaration`; `null` means undeclared (normal for standalone OSS and synthetic example configs). |

### JSON Schema fragments

Producers embed these property definitions (JSON Schema 2020-12) at the top level of their output documents:

```json
{
  "generated_at": {
    "description": "RFC 3339 UTC timestamp recording when the producer finished observing.",
    "type": "string",
    "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]{1,9})?Z$"
  },
  "observed": {
    "description": "Whether the producer completed at least one first-hand observation. When false, the document carries no first-hand observation and carries no verdict colour.",
    "type": "boolean"
  },
  "valid_for": {
    "description": "How long the producer stands behind the document, in integer seconds (>= 1).",
    "type": "integer",
    "minimum": 1
  },
  "declaration": {
    "description": "SHA-256 digest of the canonical watch declaration, or null when undeclared.",
    "type": ["string", "null"],
    "pattern": "^sha256:[0-9a-f]{64}$"
  }
}
```

Existing producers that emit second-precision RFC 3339 UTC timestamps (`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`) remain conformant.

Producer and consumer config schemas admit the optional top-level `declaration` key using the same `["string", "null"]` and `^sha256:[0-9a-f]{64}$` constraint.

### `observed: false` per-tool semantics

When `observed` is `false`, the document carries no first-hand observation and carries no verdict colour. Each witness defines `observed: false` as follows:

| Witness | `observed: false` condition | Notes |
| --- | --- | --- |
| `scry` | Probe could not observe cluster | Existing meaning unchanged. |
| `simplex` | Every check is `unknown` | Vantage is blind. |
| `cubit` | Exit `2` / no configured root usable | Scanner could not inspect any declared root. |
| `augur` | Alertmanager unreachable or poll failed | No fresh alert poll completed. |

### `valid_for` rules

- **Integer seconds only.** `valid_for` is always an integer `>= 1` representing seconds — **never** duration strings (such as `"60s"`) or floating-point numbers — so canonical JSON and contract validators remain free of unit and float formatting.
- **`serve` modes.** Continuous `serve` modes emit `valid_for = 2 * interval`, tolerating one missed pass at the source.
- **One-shot commands.** One-shot commands accept `--valid-for` (integer seconds `>= 1`) with a documented per-tool default.
- **Consumer tightening.** Effective freshness in Vigil is `min(valid_for, stale_after)`. Vigil's configuration may tighten a producer's promise, never extend it.

### `declaration` scope

- **Verbatim copy.** Producers copy the optional config key `declaration` verbatim into every emitted document.
- **Undeclared default.** `null` (or an omitted config key emitted as `null`) means undeclared, which is normal for standalone OSS deployments and synthetic example configs.
- **Watch scope.** `declaration` is scoped to `sha256(canonical(watch))`, hashing only the watch projection rather than unrelated operational configuration. When evaluated from a larger configuration world, the emitter manifest carries `{exportHash, watchHash}` so external state stores can join a verdict to its full world without causing unrelated edits to look like watch drift.

## 3. Schema evolution and rollout law

Every witness output schema and config schema sets `additionalProperties: false` everywhere. Because strict validators and golden fixtures reject unknown properties, adding header fields follows a strict three-step order:

1. **Admit.** Add the optional output fields (`observed`, `valid_for`, `declaration`, plus `generated_at` where not already present) to the producer's output schema (listed under `properties`, **not** in `required`), and add optional `declaration` to its config schema (accepted by `validate`). Update internal fixtures and goldens.
2. **Re-vendor.** Update each consumer's vendored copy of the upstream schema to the admitted version, recording the upstream source commit SHA and SHA-256 digest.
3. **Emit.** Update producers to emit all four header fields on every document.

Within a single repository, Admit and Emit may land in one PR once internal fixtures and goldens are updated. Across repositories, consumer Re-vendor strictly follows the producer's merge to `main` so live documents are never rejected by a stale vendored schema.

### Compatibility rules (`v0`)

- **Version keys.** Preserve `schema` in `scry` and `apiVersion` in the other four (`simplex`, `augur`, `cubit`, `vigil`). Do not rename version keys in `v0`; Vigil dispatches on each tool's existing key.
- **Holds.** Existing per-tool hold spellings remain unchanged in `v0`. Higher-level config emitters project one declaration into each tool's native hold spelling rather than breaking existing `v0` Go config schemas.
- **Simplex tree-gate boundary.** Each producer documents its own header fields neutrally without naming sibling tools inside its repository. This file in `vigil/docs/` is the family-level normative reference.

## 4. Consumer freshness and drift summary

Downstream V2 summary evaluation applies pure functions over stored witness documents and Vigil's local clock:

- **`received_at`.** Vigil records its local clock at the first sighting of a document with a new `(generated_at, sha256(body))` pair. A file whose timestamp and body do not change keeps its original `received_at` and ages normally.
- **Dual freshness check.** A document is fresh if and only if both conditions hold:
  - `now - received_at <= eff_valid_for`, where `eff_valid_for = min(valid_for, stale_after)` (prevents a fast producer clock from extending its lifetime);
  - `now - generated_at <= eff_valid_for + skew_budget` (catches a frozen producer clock repeatedly emitting an old `generated_at`).
- **Informational `clock_skew`.** Vigil emits a per-source `clock_skew` finding when `|received_at - generated_at| > skew_budget` (configurable, default `10s`). `clock_skew` is informational only: it never turns a stale document fresh and never colours `overall` by itself.
- **Reference-based `declaration_drift`.**
  - The reference hash is Vigil's own `declaration` from its config (never a majority vote across witnesses).
  - Drift occurs when any fresh, declared witness document carries a `declaration` differing from Vigil's reference.
  - Within the configured grace window (`declaration_grace_s`, default `2 *` the longest producer interval) after Vigil's own `declaration` changes, drift is recorded as an informational fact; after the grace window expires, drift maps to `YELLOW` and never `RED`.
  - Documents with `declaration: null` are counted as `undeclared`, not as drift.
  - Summary output lists the subjects observed on each declaration hash.
