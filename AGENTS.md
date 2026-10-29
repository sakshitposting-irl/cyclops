# cyclops - guide for AI agents and contributors

cyclops is a Kubernetes controller that sends a report of cert-manager `Certificate`s that are
expired, never issued, or overdue for renewal. The human-facing rules are in
[CONTRIBUTING.md](CONTRIBUTING.md); this file adds what an agent needs to work in the code.

## Read first

- [`docs/decisions/README.md`](docs/decisions/README.md) indexes every architectural decision (ADR)
  and what is still open. Do not make a new architectural call (data model, watch strategy,
  delivery, packaging) without checking it there, and do not implement around an open item: flag it.
- [`docs/internals/`](docs/internals) describes how the code works. Update it when you change the
  behaviour it describes.

## Layout

```
api/v1alpha1/certreport_types.go   The CertReport CRD schema (+kubebuilder markers)
cmd/main.go                        Manager (controller) entry point
cmd/report.go                      Report mode: what the CronJob's Pod runs (`cyclops report`)
internal/controller/               Reconciles a CertReport into a CronJob, Jobs, a state ConfigMap, conditions
internal/certmanager/              Lists cert-manager objects and turns them into plain status values
internal/report/                   Decides what is reported, and why
internal/render/                   Email templates (html/template) and the template data contract
internal/email/                    SMTP and SES senders
config/                            Kustomize manifests (CRD, RBAC, manager)
charts/cyclops/                    The Helm chart, the supported install path
test/cluster, test/e2e             Cluster tests (kind); see Testing
```

The shape to keep in mind: the controller never reads certificates and never sends mail. It keeps
a `CronJob` in sync with the `CertReport`, and the CronJob's Pod (same binary, report mode) does
the reading and the sending. The two run under separate ServiceAccounts, and the report Pod is
read-only.

## Generated files: do not edit by hand

| File | Regenerate with |
|---|---|
| `config/crd/bases/*.yaml`, `config/rbac/role.yaml` | `make manifests` |
| `api/v1alpha1/zz_generated.deepcopy.go` | `make generate` |
| `charts/cyclops/templates/crd.yaml` | `make helm-crd` |
| `charts/cyclops/files/default.html.tmpl` | `make helm-crd` (copied from `internal/render/default.html.tmpl`) |
| `charts/cyclops/files/certreport-example.yaml` | `make helm-crd` (copied from `config/samples/cyclops_v1alpha1_certreport.yaml`, which the CRD test validates) |
| `PROJECT` | the kubebuilder CLI |

After editing `api/v1alpha1/*_types.go` or any `+kubebuilder` marker, run `make manifests generate`
and then `make helm-crd`. The report Pod's RBAC is not generated: it is written by hand in
`config/rbac/report_role.yaml` **and** in `charts/cyclops/templates/rbac.yaml`, so change both.

Keep the `// +kubebuilder:scaffold:*` comments; the kubebuilder CLI injects code at them.

## Rules specific to this project

- **Cluster-scoped, one Kind.** `CertReport` is the only API type (ADR 0002). Do not scaffold new
  kinds with `kubebuilder create api`.
- **No finalizers.** Everything a `CertReport` creates is owned by it through an owner reference,
  so deleting it cleans up.
- **Credentials never go in the CR or in chart values.** Mail credentials live in a Secret the
  operator creates, and reach the report Pod as environment variables (ADR 0014).
- **cert-manager's text is relayed verbatim.** Failure `Reason`s and issuer errors are shown word
  for word, never categorised (ADR 0004, 0021).
- **Inclusion is by dates only.** Diagnostics are shown but never cause a certificate to be
  reported (ADR 0009, 0012).
- **Template data only grows.** `TemplateData` and `Row` are a contract for custom templates, so
  changes must be additive (ADR 0013). Treat cert-manager text in templates as untrusted.
- **Do not run the kubebuilder helm plugin with `--force`.** It would overwrite the hand-written
  chart (ADR 0016).

## Development

```sh
make build         # build bin/manager
make test          # unit tests (envtest: a real API server and etcd, downloaded on first run)
make lint          # golangci-lint; `make lint-fix` applies autofixes
make helm-lint     # lint and render the chart (needs helm)
make test-cluster  # cluster validation on a kind cluster named kind-cyclops (build tag `cluster`)
make help          # every target
```

Run one package or test with `go test ./internal/email/... -run TestXxx`. Always run `make test`
and `make lint` before a pull request.

## Testing

- `internal/controller` uses Ginkgo and Gomega on envtest; see `suite_test.go`. The other packages
  use the standard `testing` package with table tests.
- Bug fixes come with a test that fails without the fix.
- `test/cluster` and `test/e2e` need a kind cluster. The e2e tests must run against a dedicated
  kind cluster, never a real dev or production cluster.
- Mail is tested against in-process fakes (`internal/email/fakesmtp_test.go`) and, on kind, against
  MailHog (`hack/kind/mail.yaml`).

## Conventions

- Match the surrounding code: naming, comment density and idiom. Comments explain why and cite the
  ADR when one decided the behaviour.
- Use `metav1.Condition` for status, and idempotent reconciliation. Watch secondary resources with
  `Owns()` or `Watches()` instead of relying on `RequeueAfter`.
- Logging is structured (`log.Info("Message", "key", value)`); follow the style of the log lines
  next to yours.
- Commit messages and pull requests: see [CONTRIBUTING.md](CONTRIBUTING.md). A change that makes
  the README status table, the chart README or an ADR stale updates them in the same pull request.

## References

- [Kubebuilder book](https://book.kubebuilder.io) and its [markers reference](https://book.kubebuilder.io/reference/markers.html)
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md)
- [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
