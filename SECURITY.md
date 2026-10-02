# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it privately via
[GitHub Security Advisories](https://github.com/truvity/cd/security/advisories/new).

Do NOT open a public issue for security vulnerabilities.

## Supported Versions

Only the latest release is supported with security updates.

| Version | Supported |
|---------|-----------|
| latest  | yes       |
| older   | no        |

## Design notes relevant to a reviewer

- These charts wrap software that holds the keys to a cluster's delivery.
  What they *default* is a security surface, so they default nothing: the
  render with no values is the upstream chart's own, and every opening
  (a route, an account, a sign-in) is the caller's value.
- They hold no credentials and no hostnames. An installation's identity
  provider, client identifiers and repository credentials belong to the
  caller's values and Secrets; `hack/leak-canary.sh` fails the build on the
  mechanical shapes of an estate's particulars.
- Upstream versions are pinned exactly and the archive is vendored, so a
  render never depends on what a registry serves today.
