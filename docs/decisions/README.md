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
| 9 | Report inclusion: by expiry only; failure diagnostics shown but never cause inclusion | [0009-report-inclusion.md](0009-report-inclusion.md) |

## Open / not yet decided

- Credential handling specifics for SES vs SMTP (IAM vs access keys vs
  SMTP host/user/pass/TLS, Secret shapes).
- State & dedup: how to avoid re-alerting on the same cert every single day
  once it crosses the threshold, and where that state lives.
- RBAC / watch scope details (namespace filtering mechanics, ClusterRole
  shape).
- HA / leader election posture for the controller (the reconciler managing
  the CronJob is lightweight, but still worth deciding explicitly).
- Observability: metrics/logging expectations.

## Ground rules for this project

- No implementation until the relevant decisions are made and confirmed.
- Every decision is backed by pros/cons, not just picked.
- When there's real uncertainty (e.g. unfamiliar API behavior, unclear
  prevalence of a problem), research/read together before deciding rather
  than guessing.
