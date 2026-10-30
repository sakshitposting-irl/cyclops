# 16. Packaging: a Helm chart, published as an OCI artifact

## Status
Decided (2026-10-30). Records what was built and released as `v0.1.0-beta.7`.
Not settled here: whether the kustomize manifests in `config/` stay a second
supported install path. The README says they still work.

## Context
ADR 0014 sets mail delivery once per install, in Helm values rendered into a
ConfigMap, with `values.schema.json` validating them. That needs a chart. The
project began with kubebuilder's kustomize `config/`, and kubebuilder's helm
plugin can generate a chart from it.

## Options considered

### 1. Where the chart comes from

**A. Generate it with kubebuilder's helm plugin**
- Pros: no chart to maintain by hand.
- Cons: tested with kubebuilder 4.16 `helm/v2-alpha` on a scratch clone. A
  plain regenerate overwrites its own generated templates (manager, rbac,
  crd) and keeps `values.yaml`, our own template directories and
  `values.schema.json`; `--force` also resets `values.yaml`. Running it
  would put hand-written ADR 0014 work at risk.

**B. A hand-written chart in this repo, at `charts/cyclops`** *(chosen)*
- Pros: it carries ADR 0014's values and `values.schema.json`, and the
  report Pod's RBAC, which is not generated (see `AGENTS.md`).
- Cons: the chart mirrors the kustomize install (same RBAC, flags and names
  with the release prefix), so a change in one has to reach the other. The
  CRD template is copied from `config/crd/bases` by `make helm-crd` to keep
  that from drifting. Do not run the helm plugin with `--force`.

### 2. How it is distributed

**A. A classic Helm repository** (an `index.yaml` served over HTTPS)
- Cons: somewhere to host and update the index.

**B. An OCI artifact in GHCR** *(chosen)*
- Pros: the chart sits beside the image in the same registry, and the same
  workflow pushes both. `release.yml` runs on a `v*` tag, packages the chart
  with its `version` and `appVersion` taken from the tag, and pushes it to
  `oci://ghcr.io/<owner>/charts/cyclops`.
- Cons: installing needs a Helm version with OCI support.

## Decision
- Helm is the supported install path. The chart is hand-written, at
  `charts/cyclops`, and CRDs live in `templates/` with `crd.enabled` and
  `crd.keep` (`helm.sh/resource-policy: keep`).
- It is published as `oci://ghcr.io/sakshitposting-irl/charts/cyclops`, and
  its version is the release tag without the leading `v`. The values in
  `Chart.yaml` are placeholders that the release overrides.

## Still open
- Whether `config/` stays a second supported install path, or is only
  development tooling (`make deploy`, CRD generation).
