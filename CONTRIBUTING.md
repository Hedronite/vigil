# Contributing to Vigil

Vigil is a Go query surface that combines the verdicts of named authorities into one document. Read `docs/semantics.md` first: it states what `overall` means and which rules are fixed.

## Sign off every commit (DCO)

This project uses the Developer Certificate of Origin. Add a sign-off line to every commit:

```
git commit -s
```

That appends `Signed-off-by: Your Name <you@example.invalid>` using your git identity. A pull request with an unsigned commit is not merged. Fix it with a new signed commit or a rebased branch of your own; never rewrite a branch someone else has pulled.

By signing off you certify the following.

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

Contributions are licensed under Apache-2.0, the license of this repository.

## Build and check

Go 1.27.4 is pinned in `go.mod`. Run these before you push; CI runs the same on Linux and macOS.

```
go vet ./...
golangci-lint run ./...
govulncheck ./...
go test -race -shuffle=on -count=1 ./...
scripts/scrub.sh
scripts/scrub.sh --selftest
scripts/depguard-selftest.sh
scripts/fuzz.sh --selftest
scripts/fuzz.sh
```

Use golangci-lint v2 and a current govulncheck. All checks are blocking.

## Rules that are not negotiable

- **No URL input.** No flag, route, config value or document field makes Vigil fetch a URL, take a selector, or send free-form PromQL.
- **No `os/exec`** in non-test code, and no schema validator in the binary. The linter enforces both.
- **One HTTP client.** Every call to a backend goes through `internal/httpx`: GET only, no redirect, no proxy, a body cap, a deadline, two calls in flight per backend. The linter rejects `net/http` anywhere else in non-test code.
- **Severity comes from authorities.** Vigil combines verdicts and applies the stale and missing-authority caps. It does not compute severity from raw data.
- **Aliases only.** Fixtures, docs and examples use neutral host aliases (`core`, `spoke`, `depot`, `annex`, `lab`, `edge`) and `example.invalid`, never real machine names. No IP addresses, hostnames or secrets. Placeholders use reserved documentation values. An operator's real slugs and addresses live only in their untracked, off-repo overlay.
- **Schemas are the contract.** JSON Schemas under `schema/` are written by hand. A change that adds a field is additive within `v0`; anything that removes or renames one needs a new major.
- **Hermetic tests.** Table-driven, `httptest` for every backend, no live network, no wall clock (inject the clock).

## Pull requests

1. Branch from `main`. Keep a branch to one slice of work.
2. Write the commit message as what changed and why. Do not name machines or people in commit or pull request text.
3. Add or update the fixture and test that prove the change, and update `docs/semantics.md` if a rule moves.
4. Open the pull request. It merges when CI is green on both operating systems and a maintainer has reviewed the exact head.
5. Fix review comments with new commits.

Report vulnerabilities privately, as described in `SECURITY.md`.
