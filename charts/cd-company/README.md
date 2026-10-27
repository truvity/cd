# cd-company

A company delivery adapter: platform-owned facts plus company-owned pins and
ring values become the Applications rendered by `cd-delivery.applications`.
The chart has no installation defaults. It is published as
`oci://ghcr.io/truvity/charts/cd-company` with the same release version as the
other `cd` charts and `github.com/truvity/cd`.

## Ownership and files

| Input | Owner | Contract |
| --- | --- | --- |
| `platform` | Platform | The [cd-delivery platform facts](../../docs/reference.md#cd-delivery). |
| `products` | Platform | Product facts keyed by service, with the same schema as cd-delivery except `pin` and `values`, which belong to the company inputs. |
| `services` | Platform | Unique service-name allowlist. |
| `pins` | Company | Every allowed service has a string pin. `""` is dormant; a missing key is an error. |
| `values` | Company | Optional service map, each containing only `infra`, `app`, `e2e` object payloads. |

The company repository (for example, `example-org/company`) contains:

```text
pins/example-service/devel.yaml
pins/example-service/stage.yaml
pins/example-service/prod.yaml
values/example-service/devel.yaml
values/example-service/stage.yaml
values/example-service/prod.yaml
```

```yaml
# pins/example-service/devel.yaml
pins:
  example-service: "1.2.3"
```

```yaml
# values/example-service/devel.yaml
values:
  example-service:
    infra:
      postgres:
        parameters: {max_connections: "100"}
    app:
      config: {greeting: hello}
    e2e:
      config: {timeout: 30s}
```

Company files carry only their `pins` or `values` envelope. The platform caller
enforces those file boundaries and supplies the authoritative facts; a Helm
chart sees the merged values, not which repository supplied each file.

For example, the platform supplies this complete `platform.yaml`:

```yaml
platform:
  clusterName: devel
  argocdNamespace: argocd
  applicationPrefix: devel-
  appProject: "{product}-{cluster}"
  chartRegistry: oci://registry.example.com/charts
  postgres: {instances: 1, storage: 2Gi}
  events:
    url: nats://broker.example.com:4222
    storage: file
    replicas: 1
    maxAge: 24h
    account: "{product}"
products:
  example-service:
    interface: 15
    hostname: example-service.example.com
services: [example-service]
```

```sh
helm template company charts/cd-company \
  -f pins/example-service/devel.yaml \
  -f values/example-service/devel.yaml \
  -f platform.yaml
```

For each non-dormant service the adapter deep-copies its product facts, sets
`pin` from `pins`, replaces `values` with that service's payload (empty if
absent), and calls the existing delivery named template. Its packaged cd-delivery
dependency keeps its own `products` empty, so it renders no second copy.
Company payloads merge over the composed
ring values using cd-delivery's existing merge semantics; unrelated nested
business configuration survives. The pinned child charts validate the complete
result against their own schemas.

## Refusals

- Unknown envelope/platform/product keys, unknown rings, invalid service names,
  duplicate allowlist entries and non-string pins fail schema validation.
- Every `pins`, `values` and `products` key must be allowed by `services`.
  Every allowed service needs a pin key; every non-dormant service needs product
  facts. A dormant service emits no Applications, but its payload is still checked.
- The dependency's `cd-delivery.products` must remain empty. Product composition
  belongs to the parent adapter inputs; setting subchart products is refused.
- At each ring's delivery-value root, company payloads cannot set `cloud`,
  `events`, `identity`, `route`, `serviceAccount`, `tier`, `pin`, `chart`,
  `repository`, `interface`, `appProject`, `installName`, `availability`,
  `database`, `tls`, `archive`, `otel`, `access`, `identityProviders` or `alerts`.
  `web.faro` and `observability.dashboards` are also platform-owned.
- The platform's postgres fields are reserved: `platformOwned`, `instances`,
  `storage`, `runtimeRole`, `runtimePasswordSecret`, `runtimePassword`, `labels`,
  `scheduling`, `backup`, `serverTLS`. Other postgres tuning can be passed through.
- The e2e ring additionally reserves `appRelease`, `mode`, `traces`,
  `job.annotations`, `job.ttlSecondsAfterFinished`, `prober.enabled`,
  `prober.interval` and `prober.serviceAccount`.

Replacing a reserved subtree with a scalar or array also fails. These checks
are path-based: `app.config.cloud` is application configuration, not a delivery
cloud override. New delivery-owned fields must be reflected in the ownership
checks when the delivery interface moves.

## Pins, promotions and values

Kargo writes both normal promotion pins and rollback pins. `devel` promotions
write directly; `stage` and `prod` promotions use PRs. Human pin edits are
emergency-only. Empty pins leave allowed services dormant until promoted.

Humans change ring values through check-gated PRs. Once a values PR merges,
Argo CD applies the changed values at the existing pin; no separate Kargo
promotion is needed. Platform-owned CI validates the candidate against the
authoritative platform inputs with [chartgate](../../chartgate/README.md).

## Maintaining the adapter

`cd-delivery` is a normal local Helm dependency (`file://../cd-delivery`),
maintained with `just vendor cd-company`. Commit `Chart.lock` and the resulting
`charts/cd-delivery-0.0.0.tgz` archive. Its templates have one editable source:
`charts/cd-delivery`. The dependency's default `products: {}` renders nothing;
the adapter schema enforces that this map stays empty.

`python3 hack/company-schema.py` derives `values.schema.json` from cd-delivery's
strict platform/product schemas; it does not copy chart templates. Commit that
schema too. `just lint` checks schema drift and the normal dependency status.
`just charts` and `just test` verify the archive matches the cd-delivery source,
prove parity with direct composition, and compare goldens.
