<!-- go-tops:begin -->
# AGENTS.md — Go-TOPS Agent Contract

This file is the contract between this repository and any AI coding agents (or human reviewers) working on it.

**Class:** `binary-service`
**Protocol:** Go-TOPS 0.4.4
**Module:** `github.com/Hedronite/vigil`
**Enforce:** `ratchet`

## Commands

```bash
# Validate config
go tool -modfile=tools/go.mod gotops check

# Run the merge gate (tier ≤ 1)
go tool -modfile=tools/go.mod gotops gate

# Full reports
go tool -modfile=tools/go.mod gotops report --format text
```

## Floors (this class)

- Statement coverage ≥ `75%`
- CRAP cap ≤ `10`, the same for every author
- Error guards that only return the error count 0 toward complexity (`err_guard_discount: true`)
- Unexplained mutation survivors: none (`unexplained_survivors_max` is 0), on lines changed against `origin/main`
- Race detector: required (test jobs run with `CGO_ENABLED=1`; `-race` needs cgo on Linux)
- Under `ratchet`: a function over the cap may not exceed its score in `.go-tops/baseline.json` plus 0.01, and every other function meets the cap. A package under its floor fails only if its coverage percentage falls and its uncovered-statement count rises. No per-file count of lint findings, law findings, `//nolint` or `//tops:ack` comments, or unexplained survivors, and no repo exemption count, rises above its baseline entry. Only `gotops baseline` writes the baseline
- Other class layers: see `go-tops.yaml`

## Policies (non-negotiable)

1. Write the oracle from the contract before the code.
2. Never split a function only to lower its CRAP score (scar-guard).
3. Never weaken a law, linter config, or threshold to make a change pass.
4. Explain every accepted mutation survivor (reason, owner, expiry).
5. No `//nolint` without the linter name and a reason; no `//tops:ack` without a reason and an owner.
6. Agent-authored code meets the same gate and the same CRAP cap as human code.
7. Do not edit generated files; fix the generator.
8. Prefer deleting code to adding configuration.
9. Never edit `.go-tops/baseline.json` by hand, and never wrap old findings in `//nolint` or `//tops:ack` to zero a count.

## Common Agent Mistakes to Attack (explicit targets)

When generating or reviewing code, specifically check for and reject the following high-frequency failure modes. The check that enforces each rule is in brackets. **[review]** means no tool catches it; the reviewer must.

### Error Handling

- **Never** discard errors with `_`. **Bad:** `_, _ = f.Write(data)`, `x, _ := strconv.Atoi(s)`. **[errcheck `check-blank`, G-12]**
- Check the error from `Close`, `Flush` and `Sync` on anything you wrote to. Use a named return and a deferred check that keeps the first error. A deferred `Close` on a reader passed as `io.ReadCloser` (a response body) passes the errcheck `exclude-functions` list, which holds only `(io.ReadCloser).Close`. A read-only file from `os.Open` takes a `//nolint:errcheck` with a reason, which is counted (§6.7 `suppressions`); `_ = f.Close()` fails. A file you wrote to is checked by review. **[errcheck, G-12; review]**
- Always wrap returned errors with operational context: `fmt.Errorf("action %s: %w", id, err)`. **[errorlint, G-11]**
- Use `errors.Is` / `errors.As`. Never match on `err.Error()` strings or direct equality on wrapped errors. **[errorlint, G-11]**
- Do not `panic` or call `Must*` helpers for recoverable conditions in library code. **[G-13]**

### Concurrency & Context

- Never spawn unbounded goroutines. Use `errgroup.WithContext` + `SetLimit` for concurrent work. **[review; G-01 heuristic]**
- Always accept `ctx context.Context` as the first parameter on I/O, DB, and network functions. **[revive `context-as-argument`, G-15, G-16]**
- Never introduce `context.Background()` or `context.TODO()` inside business logic (only allowed in `main`, wiring, and tests). **[G-17]**
- Never pass structs containing `sync.Mutex` or `sync.WaitGroup` by value. **[vet `copylocks`, G-07]**
- Prefer channels for orchestration/ownership transfer; prefer mutexes for protecting shared in-memory state. **[review]**
- No `time.Sleep` in tests to wait for concurrency. Use `testing/synctest` or channels; do not poll. On Go 1.27 or later, inside a `synctest` bubble use `synctest.Sleep(d)` (advances the fake clock, then waits until every other goroutine is durably blocked) and `httptest.NewTestServer(t, handler)` (an in-memory network). **[G-08]**

### Idiomatic Design

- Accept interfaces, return concrete structs. Define small (1–3 method) interfaces in the **consumer** package, not alongside implementations. **[review]**
- Avoid premature `any` / `interface{}` and reflection. Prefer typed structs or constrained generics. **[review]**
- Use the modern standard library: `os.ReadFile`, `slices`, `maps`, `errors`, `math/rand/v2`. Do not use deprecated `io/ioutil`. **[staticcheck deprecation checks]**

### Resources & Performance

- Always set timeouts on `http.Client` / `http.Server`. Never use `http.DefaultClient` in production code. **[G-19]**
- Bound untrusted reads (`io.LimitReader`, `http.MaxBytesReader`). Use `io.ReadAll` only on a bounded reader. **[G-20 heuristic]**
- Close response bodies, rows, files, and timers. Check `rows.Err()`. **[bodyclose, sqlclosecheck, rowserrcheck, G-18]**
- Preallocate slices when the length is known (`make([]T, 0, len(in))`). **[prealloc]**
- Prefer `strings.Builder` over `+=` in loops. **[review]**

### Testing

- Prefer table-driven tests with `t.Run`. **[review]**
- Call `t.Helper()` in test helpers so failures report the caller line. **[thelper]**
- Use `t.Cleanup` for teardown. **[review]**
- Tests must be deterministic: no wall-clock dependence, no unseeded randomness, no live network. **[G-31 heuristic]**

## Verification (before finishing any task)

```bash
go tool -modfile=tools/go.mod gotops gate
```

The gate runs hygiene first (tidy, verify, the toolchain pin, formatting, lint, vulnerabilities, licenses), then the test suite once, with the race detector, shuffling and coverage; coverage, CRAP and mutation read that run. The laws run last. Do not run them separately and call that done. Resolve all failures. A green gate is the definition of done.

---

*Generated by `gotops init`. Update via the protocol change process if laws or floors change.*

<!-- go-tops:end -->
