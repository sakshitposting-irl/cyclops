# Architecture Decision Records

This folder tracks the architectural decisions made for cyclops, in the order
they were made. Each file is a lightweight ADR: context, options considered
(with pros/cons), and the decision. Decisions are made deliberately, one at a
time, before the code that depends on them is written. How the resulting code
works is described in [`../internals/`](../README.md#internals).

## Decided

| # | Decision | File |
|---|---|---|
| 1 | Language & controller framework: Go + controller-runtime/kubebuilder | [0001-controller-framework.md](0001-controller-framework.md) |
| 2 | CRD scope: cluster-scoped, single `CertReport` Kind | [0002-crd-scope.md](0002-crd-scope.md) |
| 3 | What we watch: cert-manager `Certificate` resources, no usage/attachment filtering | [0003-cert-watch-target.md](0003-cert-watch-target.md) |
| 4 | Failure diagnostics: structured fields + raw `Reason` passthrough, no message categorization | [0004-failure-diagnostics.md](0004-failure-diagnostics.md) |
| 5 | Email templating: default `html/template`, overridable via ConfigMap reference | [0005-email-templating.md](0005-email-templating.md) |
| 6 | Scheduling model: controller reconciles CR and owns a child `CronJob` | [0006-scheduling-model.md](0006-scheduling-model.md) |
| 7 | Notification channels: typed `notifiers` list in the CRD now; only `Email` implemented in v1, `Webhook` deferred to v2 | [0007-notification-channels.md](0007-notification-channels.md) |
| 8 | Email providers: both SES and SMTP in v1 | [0008-email-providers.md](0008-email-providers.md) |
| 9 | Report inclusion: by dates only; failure diagnostics shown but never cause inclusion (threshold superseded by 12) | [0009-report-inclusion.md](0009-report-inclusion.md) |
| 10 | Watch scope: `spec.namespaces` list (empty = all); read-only ClusterRole (missing-namespace handling superseded by 11) | [0010-watch-scope.md](0010-watch-scope.md) |
| 11 | Namespace validation: controller sets a `NamespacesFound` condition and watches Namespaces; not in the email | [0011-namespace-validation.md](0011-namespace-validation.md) |
| 12 | Inclusion rule: never issued, expired, or past own `renewalTime` by >1h; no threshold | [0012-renewal-overdue.md](0012-renewal-overdue.md) |

## Open / not yet decided

- Diagnostics for non-ACME failures: ADR 0004 takes `State`/`Reason` only
  from ACME Orders/Challenges, so a certificate from a CA, Vault or
  self-signed issuer that fails to issue is reported with no reason at all.
  Found by the cluster tests. Checked on the cluster: the Certificate's own
  `Ready` condition doesn't help (`DoesNotExist` refers to its TLS Secret,
  normal before first issuance); the newest CertificateRequest's `Ready`
  condition points at the issuer; the Issuer's `Ready` condition has the
  root cause (e.g. `ErrGetKeyPair`: CA Secret not found). Options: relay the
  request's condition, or the request's plus the Issuer's (needs read RBAC on
  Issuers/ClusterIssuers, revising ADR 0010).

- Credential handling specifics for SES vs SMTP (IAM vs access keys vs
  SMTP host/user/pass/TLS, Secret shapes).
- State & dedup: how to avoid re-alerting on the same cert every single day
  once it crosses the threshold, and where that state lives.
- HA / leader election posture for the controller (the reconciler managing
  the CronJob is lightweight, but still worth deciding explicitly).
- Observability: metrics/logging expectations.

## Ground rules for this project

- No implementation until the relevant decisions are made and confirmed.
- Every decision is backed by pros/cons, not just picked.
- When there's real uncertainty (e.g. unfamiliar API behavior, unclear
  prevalence of a problem), research/read together before deciding rather
  than guessing.
