# Contributing

Thanks for looking. This repository holds Helm charts that install Argo CD
and Kargo and nothing else; what an installation is called, who signs in to
it and where it runs are the installer's values.

## Before you open a pull request

```sh
devbox shell
just check      # lint, goldens and the zero-diff gate, leak canary
```

`just check` is exactly what CI runs on a pull request.

## What belongs here

Mechanism. If a value would be different in another organisation, it is an
input and has no default. `hack/leak-canary.sh` catches the mechanical
cases; review catches the rest.

## Rules of the road

- A chart's render with no values is the upstream chart's. A change that
  moves it fails `just test` on purpose: declare it as a
  `**Behaviour change` bullet in CHANGELOG.md and regenerate the goldens
  (`just golden`), reading the diff.
- Moving an upstream pin is `just vendor <chart>`, then `just golden`, then
  appVersion to the upstream's (`just lint` checks it). Read the upstream's
  own changelog between the two pins first.
- Commits are small and say why. The default branch is `master`; pull
  requests merge by rebase.

## Reporting a vulnerability

Privately, as described in [SECURITY.md](SECURITY.md). Not as an issue.
