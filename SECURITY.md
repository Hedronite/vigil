# Security policy

## Reporting a vulnerability

Report privately through GitHub: open the **Security** tab of this repository and choose **Report a vulnerability**. That opens a private advisory visible only to the maintainers and you.

Please do not file a public issue or pull request for a vulnerability, and do not post details in public channels before a fix ships.

A useful report has the version or commit, what you did, what you expected, what happened, and the smallest input that shows it. A fixture or a failing test is the best form.

We acknowledge a report as soon as practical, and agree a disclosure date with you once the fix is understood.

## Supported versions

Vigil has no tagged release yet. The `main` branch is the only supported line. After the first tag, the latest minor release and `main` are supported.

## What counts

Anything that breaks a boundary in the table below is a security bug, including a way to make Vigil:

| Boundary | What must hold |
| --- | --- |
| No outbound surprises | Vigil only contacts backends named in its config. No command, route, config value or document field makes it fetch a URL, run a selector, or send PromQL it was not configured with. |
| No program execution | Vigil never runs another program. Authorities publish files; Vigil reads them. |
| Auth fails closed | Every `/v0/*` route needs the read token and `POST /v0/signals` needs the write token, on every bind. A missing or wrong token gets the same response. A missing token file, or one readable by others, stops startup. |
| Loopback by default | The default bind is `127.0.0.1`. Wildcard binds are always refused. |
| Signals are not verdicts | A signal can never change `overall` or the exit code. |
| No address leaks | Output and fixtures carry host slugs only: no addresses, hostnames or secrets. |
| Bounded input | Request bodies, file reads and backend responses have size caps and timeouts. |

Also in scope: a decoder crash or hang on crafted input, a way to read or write outside the configured paths, and a way to show a stale or unavailable source as current.

Out of scope: findings that need an attacker who already holds the read or write token for what that token allows, a local user with shell access on the host Vigil runs on, and denial of service by a caller the operator has let onto the listener.

## Handling secrets

Tokens and overlays never enter the repository. Tokens live in files outside the tree, passed by path; overlays map backend ids to addresses and are untracked. CI runs a scrub over every tracked file for addresses, hostnames and token shapes. If you find a secret in the history, report it the same way as a vulnerability.

## Fixes

A fix lands as a pull request with a test that fails before and passes after. Where a report changes a documented boundary, the change is recorded in `docs/semantics.md`.
