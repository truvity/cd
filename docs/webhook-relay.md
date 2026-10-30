# webhook-relay

A fan-in relay for GitHub webhooks, placed in front of Argo CD's
`/api/webhook`. Chart: `cd-webhook-relay`; image:
`ghcr.io/truvity/cd/webhook-relay`.

## Why

Argo CD verifies every GitHub delivery against one secret,
`webhook.github.secret` (it builds a single GitHub parser). An installation
whose Applications pull from repositories in several GitHub organisations,
each organisation's hook (or GitHub App) with its own secret that rotates on
its own schedule, cannot point them all at Argo CD. Sharing one secret across
organisations is not an answer either: it couples their rotations and lets
any one of them sign for all the others. The upstream request is
[argoproj/argo-cd#24067](https://github.com/argoproj/argo-cd/issues/24067).

## How it works

`POST /api/webhook` (Argo CD's own path, so an existing hook keeps its URL):

1. The body is read, capped at `maxBodyBytes` (default 25 MiB, GitHub's own
   limit); more is `413`.
2. `X-Hub-Signature-256` must verify (HMAC-SHA256, constant-time compare)
   against the secret of one configured sender; every sender is tried. No
   header or no match is `401`.
3. **Owner binding.** The payload is parsed just far enough to collect every
   owner it names: `repository.owner.login`, the owner part of
   `repository.full_name` and of the repository's `html_url`, `clone_url`,
   `ssh_url`, `git_url` and `svn_url`, `organization.login`, and a package's
   `namespace`. EVERY one must be in the sender's `owners` (logins compare
   case-insensitively), otherwise `403`: one organisation's secret cannot
   trigger another organisation's Applications, not even by pairing its own
   `owner` with another's URL. An event naming no owner at all is refused too
   (Argo CD acts only on events that carry a repository or a package), except
   `ping`.
4. `ping` is answered `200` and not forwarded: a rotation check pings the
   consumer with the new secret and needs a 2xx.
5. Anything else is forwarded to `target.url` with the SAME body, re-signed
   with the target secret, and the target's status is returned (`5xx` becomes
   `502`; a `4xx`, typically the target refusing the relay's signature, is
   passed through and counted as `target_error`).

The forwarded request carries `X-Hub-Signature-256` only. Argo CD v3.5.5
verifies no other header: its GitHub parser is go-playground/webhooks v6.4.0,
whose `github.Parse` reads `X-Hub-Signature-256` (the legacy SHA-1
`X-Hub-Signature` is not consulted), and its own GHCR package-event parser
reads the same header. `X-GitHub-Event`, `X-GitHub-Delivery`,
`X-GitHub-Hook-ID`, `Content-Type` and `User-Agent` are copied.

Endpoints: `GET /healthz`; `GET /readyz` (`200` once every secret file is
readable); `GET /metrics` with `webhook_relay_deliveries_total{sender,event,result}`
(`result`: `forwarded`, `ping`, `unauthorized`, `forbidden_owner`,
`too_large`, `bad_request`, `target_error`) and
`webhook_relay_forward_duration_seconds`. Logs are JSON (`log/slog`): sender,
event, delivery id and result; never a payload, a secret or a signature.
Before a delivery is authenticated its sender and event are reported as
`unknown`, so a caller cannot mint metric series.

## Secrets and rotation

Every secret is a FILE: a mounted Kubernetes Secret key. The relay stats each
file on every delivery and re-reads it when its identity, size or modification
time changed, so a rotation without overlap is honoured by the first delivery
signed with the new value, however long the kubelet took to project it. If a
re-read fails the last good value keeps serving, and `/readyz` reports it.

To rotate an organisation's secret: update that organisation's Secret, wait
for the kubelet to project it (up to about a minute), then change the hook. A
delivery signed with the old value in the meantime still verifies until the
file changes; after that it is `401`. To rotate the target secret, update the
Secret the chart names in `target.secret`; Argo CD's own
`webhook.github.secret` must carry the same value.

## Wiring

1. Install `cd-webhook-relay` with one sender per organisation:

   ```yaml
   senders:
     - name: acme
       owners: [acme]
       secret: {name: acme-webhook, key: secret}
     - name: globex
       owners: [globex, globex-labs]
       secret: {name: globex-webhook, key: secret}
   target:
     url: http://argocd-server.argocd.svc/api/webhook
     secret: {name: relay-to-argocd, key: secret}
   ```

   The installation provides every Secret; the chart creates none.
2. Give Argo CD a relay-only `webhook.github.secret` (the `relay-to-argocd`
   value above): a value no GitHub hook knows, so the only way to Argo CD's
   webhook is through the relay.
3. Point the route that serves `/api/webhook` at the relay's Service
   (port 80) instead of Argo CD's API server. Each organisation's hook keeps
   the same URL and uses its own secret.
4. Optional: `networkPolicy.enabled` (ingress from the route's pods, egress to
   Argo CD's API server and DNS) and `serviceMonitor.enabled`, both off by
   default.

## Limits

The relay is stateless and does no retries: GitHub does not redeliver a failed
webhook either, and Argo CD's polling remains the fallback. Only GitHub
deliveries are relayed.
