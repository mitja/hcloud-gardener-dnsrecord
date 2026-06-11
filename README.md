# gardener-extension-dnsrecord-hcloud

A [Gardener](https://gardener.cloud) extension that implements the **DNSRecord**
contract for provider type **`hcloud-dns`**, backed by the project-scoped
[Hetzner Cloud DNS API](https://docs.hetzner.cloud/) (`https://api.hetzner.cloud/v1`).

Gardener ships no DNS provider for Hetzner, yet shoot reconciliation hard-requires
a working DNSRecord controller for the internal domain. Hetzner moved DNS into the
**Cloud** API (project-scoped zones and rrsets), so a single `HCLOUD_TOKEN` now
covers both servers *and* DNS. This extension closes that gap.

## What it does

- Watches `DNSRecord` resources of `spec.type: hcloud-dns`.
- **Reconcile**: idempotent upsert of an rrset (create, or replace records + TTL
  if it already exists). Sets `status.zone` to the resolved zone name.
- **Delete**: deletes the rrset, treating a `404` as success.
- **ForceDelete** = Delete. **Restore** = Reconcile. **Migrate** = no-op.
- Record types: **A, AAAA, CNAME, TXT**. TTL from `spec.ttl` (default 120; the
  Hetzner API minimum is 60).
- **Zone resolution**: if `spec.zone` is set it is used; otherwise the zone is
  found by **longest-suffix match** of `spec.name` (an FQDN) against the
  project's zones. The rrset name is computed relative to the zone (apex → `@`).
- **Value normalization**: CNAME targets are made fully qualified (trailing dot);
  TXT values are double-quoted (and inner quotes escaped) unless already quoted.

## Authentication

The API token is read from the `Secret` referenced by the DNSRecord's
`spec.secretRef`, key **`hcloudToken`** (the same key the
[provider-hcloud](https://github.com/23technologies/gardener-extension-provider-hcloud)
extension uses, so one secret serves both). Key `token` is accepted as a fallback.

## Provider type

```
hcloud-dns
```

The bundled `ControllerRegistration` claims `kind: DNSRecord, type: hcloud-dns`.

## How the landscape wires it (M2)

This extension is the missing piece for fully-managed shoot DNS on Hetzner:

1. Apply `example/controller-registration.yaml` to the **virtual garden** to
   register `ControllerDeployment` + `ControllerRegistration` (`dnsrecord-hcloud`).
   Gardener installs the extension on every seed (`DNSRecord` is a `primary`,
   seed-local resource).
2. Create the `garden`-namespace domain secrets in the virtual garden:
   - `internal-domain` (`gardener.cloud/role=internal-domain`, provider type
     `hcloud-dns`, domain `internal.g.paasbox.com`, with the `hcloudToken` data key),
   - `default-domain` (`shoots.g.paasbox.com`).
3. Gardenlet `seedConfig.spec.dns.provider`: type `hcloud-dns` + the token secret.
4. Shoot manifests: drop any `unmanaged` DNS provider block — the default domain
   takes over.

## Build & test

```sh
go build ./...
go vet ./...
go test ./...

# regenerate the controller-registration after a chart change
./hack/generate-controller-registration.sh \
  charts/gardener-extension-dnsrecord-hcloud \
  example/controller-registration.yaml \
  v0.1.0
```

Container image (multi-stage, distroless/static, linux/amd64):

```sh
docker build --build-arg VERSION=v0.1.0 \
  -t ghcr.io/mitja/hcloud-gardener-dnsrecord:v0.1.0 .
```

Releases are cut by pushing a `v*` tag; the GitHub Actions workflow
(`.github/workflows/release.yml`) builds and pushes the image to `ghcr.io` and
attaches the regenerated `controller-registration.yaml` to the GitHub release.

## Binary flags

The binary `gardener-extension-dnsrecord-hcloud` mirrors the standard Gardener
extension cmd structure (controller-runtime manager, leader election, heartbeat):

| Flag | Default | Purpose |
|---|---|---|
| `--kubeconfig` | (in-cluster) | path to a kubeconfig (only out-of-cluster) |
| `--max-concurrent-reconciles` | `5` | DNSRecord controller concurrency |
| `--ignore-operation-annotation` | `false` | reconcile without the operation annotation |
| `--leader-election` | `true` | enable leader election |
| `--leader-election-id` | `gardener-extension-dnsrecord-hcloud-leader-election` | lease name |
| `--leader-election-namespace` | `kube-system` | lease namespace |
| `--heartbeat-namespace` | (none) | namespace for the heartbeat lease (set to the deployment namespace) |
| `--heartbeat-renew-interval-seconds` | `30` | heartbeat renew interval |
| `--health-bind-address` | `:8081` | health/readiness probe address |
| `--metrics-bind-address` | `:8080` | Prometheus metrics address |
| `--controllers` / `--disable-controllers` | all | enable/disable `dnsrecord`,`heartbeat` |

The Helm chart sets `--heartbeat-namespace`, `--leader-election-*` and the
concurrency/log flags from `values.yaml`.

## Version pinning rule

The `github.com/gardener/gardener` dependency is pinned **in lockstep with the
landscape** (currently **v1.122.3**, dictated by
`23technologies/gardener-extension-provider-hcloud v0.6.42`). The extensions
library API must match the running Gardener version; do **not** bump this
dependency independently of the landscape.

## Layout

```
cmd/gardener-extension-dnsrecord-hcloud/   extension binary (app + main)
pkg/hcloud/                                Hetzner Cloud DNS client, zone/name/value logic
pkg/dnsrecord/                             DNSRecord Actuator + controller wiring
charts/gardener-extension-dnsrecord-hcloud Helm chart (deployment, RBAC, SA, VPA)
hack/generate-controller-registration.sh   ControllerDeployment+Registration generator
example/controller-registration.yaml       generated registration (image v0.1.0)
Dockerfile                                  multi-stage distroless/static build
.github/workflows/release.yml              tag-triggered image + release
```
