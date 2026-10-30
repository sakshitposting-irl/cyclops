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
| 5 | Email templating: default `html/template`, overridable via ConfigMap reference, or chart-wide via `--default-template-configmap` (amendment) | [0005-email-templating.md](0005-email-templating.md) |
| 6 | Scheduling model: controller reconciles CR and owns a child `CronJob` | [0006-scheduling-model.md](0006-scheduling-model.md) |
| 7 | Notification channels: typed `notifiers` list in the CRD; `Email` plus a webhook channel (revised: originally webhook was deferred to v2; amended 2026-10-30: Slack is its own `type: Slack`, and a generic `type: Webhook` is open, see 17) | [0007-notification-channels.md](0007-notification-channels.md) |
| 8 | Email providers: both SES and SMTP in v1 | [0008-email-providers.md](0008-email-providers.md) |
| 9 | Report inclusion: by dates only; failure diagnostics shown but never cause inclusion (threshold superseded by 12) | [0009-report-inclusion.md](0009-report-inclusion.md) |
| 10 | Watch scope: `spec.namespaces` list (empty = all); read-only ClusterRole (missing-namespace handling superseded by 11) | [0010-watch-scope.md](0010-watch-scope.md) |
| 11 | Namespace validation: controller sets a `NamespacesFound` condition and watches Namespaces; not in the email | [0011-namespace-validation.md](0011-namespace-validation.md) |
| 12 | Inclusion rule: never issued, expired, or past own `renewalTime` by >1h; no threshold | [0012-renewal-overdue.md](0012-renewal-overdue.md) |
| 13 | Template data: flat `TemplateData`/`Row` contract for all templates; additive changes only | [0013-template-data.md](0013-template-data.md) |
| 14 | Email delivery: SES/SMTP settings set once per install (Helm values → ConfigMap); credentials in hand-made Secrets in the cyclops namespace, injected as env vars; SES ambient-first; strict STARTTLS; `CredentialsFound` condition (supersedes per-notifier provider in 8) | [0014-notifier-credentials.md](0014-notifier-credentials.md) |
| 15 | Scheduled runs email only when something needs attention; each `CertReport` gets a one-time welcome email (explanation only, with its own built-in template, overridable by the `welcome.html` key of the template ConfigMap; findings come in the scheduled run's own email), sent by a one-off Job, with state in a small owned ConfigMap | [0015-email-policy.md](0015-email-policy.md) |
| 16 | Packaging: a hand-written Helm chart at `charts/cyclops`, the supported install path, published as an OCI artifact to GHCR with its version taken from the release tag. Whether `config/` stays a second install path is open | [0016-packaging.md](0016-packaging.md) |
| 17 | Slack notifier (partly decided; a generic webhook and Teams are open): `type: Slack` with Slack's native format; one Secret per webhook in the cyclops namespace, injected as an env var named from the target's `name`; posts like email (findings only, plus a welcome); retries inside the process; long reports split into several messages | [0017-webhook-notifier.md](0017-webhook-notifier.md) |
| 18 | `CertReport` schema: defaulted UTC schedule with loose CRD check plus `ScheduleValid`; exposed `CronJob` settings; DNS-1123 `namespaces`; atomic `notifiers` list with type/CEL union and `minItems: 1`; required template `key` plus `TemplateValid`; printer columns Schedule/Suspended/Age. `status` beyond conditions is deferred to observability | [0018-certreport-schema.md](0018-certreport-schema.md) |
| 19 | Failed report runs: a `ReportSucceeding` condition read from the owned CronJob's status (no Job watch) | [0019-failed-run-visibility.md](0019-failed-run-visibility.md) |
| 20 | Ingress annotation mode: opt-in per CR (`spec.ingressAnnotationMode`, exclusive with `namespaces`); Ingresses join a CR with one annotation and map to Certificates by `secretName`; no cross-tenant guard in v1 (revises 10's RBAC) | [0020-ingress-annotation-mode.md](0020-ingress-annotation-mode.md) |
| 21 | Non-ACME failure diagnostics: the newest CertificateRequest's and the Issuer's/ClusterIssuer's `Ready` condition, verbatim; the report Pod reads Issuers and ClusterIssuers (revises 10's RBAC) | [0021-issuer-diagnostics.md](0021-issuer-diagnostics.md) |
| 22 | No cross-day dedup: every scheduled run reports every certificate that meets the inclusion rule, with no per-certificate state | [0022-no-dedup.md](0022-no-dedup.md) |

## Open / not yet decided

- Install paths (ADR 0016): whether the kustomize manifests in `config/`
  stay a second supported install path, or are development tooling only. The
  Helm chart itself is decided.
- Webhook notifier specifics (ADR 0007): the Slack notifier is decided in
  [0017-webhook-notifier.md](0017-webhook-notifier.md) (`type: Slack`, one
  Secret per webhook, env var named from the target's `name`, retries in the
  process, long reports split). Still open there:
  - the exact CRD field names;
  - who may use which Secret;
  - a generic `type: Webhook`, its JSON schema, auth, and Teams;
  - email's Job retry settings following the retry decision.
- HA / leader election posture for the controller (the reconciler managing
  the CronJob is lightweight, but still worth deciding explicitly).
- Observability: metrics/logging expectations. Since ADR 0015 means no
  email is sent on healthy days, a failing report run also produces no
  email. Failed runs need to show up somewhere else, e.g. a condition on
  the `CertReport` driven by the Jobs it owns.
  This also settles what `CertReport.status` holds beyond conditions (ADR 0018
  question 8), and whether `kubectl get` gets a health column.

## Where we stopped (2026-10-30): `v0.1.0-beta.7` is published and has been run against Gmail

Notes as written at the end of the day, not new decisions. The two sections below are older and
are kept as they were written.

**Published.** `v0.1.0-beta.7`: image `ghcr.io/sakshitposting-irl/cyclops:v0.1.0-beta.7` (amd64 and
arm64) and chart `oci://ghcr.io/sakshitposting-irl/charts/cyclops` version `0.1.0-beta.7`. Use it
instead of `beta.5` (crash-loops on startup) and `beta.6` (no issuer diagnostics).

**Run on kind (`kind-cyclops`) against real Gmail SMTP, from the published chart.**
- Welcome email and findings email delivered with STARTTLS on port 587 and with TLS on port 465.
- Failure path: a bad SMTP host gives `WelcomeSent=False` (reason `JobFailed`) plus one Warning
  Event, no state ConfigMap, and no retry after the host is fixed and the controller restarted.
- Not confirmed in an inbox: the ADR 0021 issuer lines in a delivered findings email.
- SES has not been run against real AWS; there is no AWS account. Only the fake client has run.

**Decided since the notes below.** Packaging, [ADR 0016](0016-packaging.md) (Helm chart, OCI on
GHCR); no dedup, [ADR 0022](0022-no-dedup.md); the Slack notifier, [ADR 0017](0017-webhook-notifier.md)
(partly: a generic webhook and Teams are open).

**Done since `beta.7`, to ship in the next release.**
- `golang.org/x/net` and OpenTelemetry bumped past the `govulncheck` findings (0 affecting).
- The inline logo now has a file name, so Gmail no longer labels it "noname".
- The unused webhook server and cross-build `make` targets are removed.
- `AGENTS.md` is a project guide and is no longer ignored, so contributors can use it.
- `helm install` prints a commented `CertReport` example and the `kubectl explain` commands.

**Next.**
- The Slack notifier ([ADR 0017](0017-webhook-notifier.md)). Only the `webhooks.Sender`
  interface exists. The CRD fields, the sender, the env var wiring and report mode are not built.
- Check the mail score of the email (HTML only, no `text/plain` part; adding one touches ADR
  0005 and 0013 and is not decided).
- Still manual-only: `test.yml` and `lint.yml`. Tidy the old `beta.3` and `beta.4` tags.

## Where we stopped (2026-10-28): `v0.1.0-beta.5` is published, nothing tested on a cluster yet

Notes as written at the end of the day, not new decisions.

**Published.** `v0.1.0-beta.5` (tag on `211fa43`): image `ghcr.io/sakshitposting-irl/cyclops:v0.1.0-beta.5`,
chart `oci://ghcr.io/sakshitposting-irl/charts/cyclops` version `0.1.0-beta.5`, and a GitHub
pre-release with the chart attached. It is the first release with the welcome email.

**Tag clean-up still to do.**
- `v0.1.0-beta.3` and `v0.1.0-beta.4` point to the same commit (`8c3e46d`) as the code in `beta.5`.
  Their release runs failed at "Create the GitHub release" because a release for the tag already
  existed (made by hand), so neither release has the chart attached. Image and chart for both
  are in GHCR. Delete the `beta.4` release and tag; keep `beta.3` only if you want its edited
  title, otherwise delete it too. `beta.5` is the one to use.
- The `beta.5` release page has GitHub's generated notes. The drafted notes (welcome email, the
  logo, "every existing `CertReport` sends one welcome after upgrading", RBAC grew, the
  `.dockerignore` fix) are not on it yet.
- The fix for the failure is in `release.yml` (`211fa43`): an existing release gets the chart
  attached with `--clobber` instead of failing. That branch has not run yet; `beta.5` took the
  "create" path.

**Next: test as an end user.**
- Use a fresh kind cluster with cert-manager. The `kind-cyclops` cluster exists but was not
  inspected; the kubectl context at the time was `kind-hfirst`, a different project, so pass
  `--context` on every command.
- Install the published chart (`--version 0.1.0-beta.5`, `email.smtp.host=...`).
- First with MailHog (`hack/kind/mail.yaml`), then SMTP with real auth/STARTTLS (a throwaway
  Gmail app password or Mailtrap), then SES if there is an AWS account.
- Check: the welcome Job appears and completes, `WelcomeSent=True`, `cyclops-state-<name>` has
  `welcomeSentAt`, the email looks right (subject, logo, schedule, namespaces). Then break it: a
  bad host gives `WelcomeSent=False` and one Warning Event, with no retry; editing the
  `CertReport` makes a new Job; restarting the controller sends nothing again.
- Check the `cid:` logo in Gmail and Outlook.

**After that.**
- Update the install version strings (README, chart README) from `beta.2` to `beta.5`, and the
  "on `main`, not in the published beta" wording about the welcome.
- Fill in the two skipped stubs in `internal/certmanager/list_test.go`
  (`TestAnnotatedIngresses`, `TestListForIngresses`); the README calls annotation mode
  "unit-tested".
- Turn on `test.yml` and `lint.yml` (both are manual-only; lint is clean locally).
- Fix the `.git/hooks/pre-commit` empty-diff bug: it rejects a commit with nothing staged
  (a "staged content matches" error from its blocked-word check). `8c3e46d` also has seven bullets against the
  three-bullet rule; it is pushed, so leave it.
- Open: release scope and ADR 0016, the webhook (ADR 0017), non-ACME diagnostics, dedup,
  HA, `IngressAnnotationMode` on `TemplateData`.

## Where we stopped (2026-10-27): welcome email and webhook

Notes as written at the end of the day, not new decisions.

**Welcome email** ([ADR 0015](0015-email-policy.md): decided and built on `main`; not yet run on a real cluster)
- "Sent" is tracked in a state ConfigMap `cyclops-state-<certreport>`, owned by the `CertReport`,
  with the key `welcomeSentAt`.
- The controller creates a one-off Job running report mode with `--welcome`. The report Pod stays
  read-only. The controller watches the Job: on success it writes `welcomeSentAt` and sets
  `WelcomeSent=True`; on failure it sets `WelcomeSent=False` (reason `JobFailed`) and emits a
  Warning Event.
- A failed welcome is retried only when the hash of the `CertReport`'s `generation` and the
  credentials Secret's `resourceVersion` changes. The Job's TTL deletes a failed Job, so the
  controller remembers the failure in the `WelcomeSent` condition (its message names the Job,
  and the name carries the hash) and does not create the Job again.
- Known gaps: delivery is at-least-once (a Job that sends and then fails, or `backoffLimit`
  retries, can send twice). A crash between Job success and the `welcomeSentAt` write is
  recovered on the next reconcile only while the Job still exists, so it depends on
  `ttlSecondsAfterFinished`. Deleting the state ConfigMap by hand sends the welcome again.
- Built: `Owns(&batchv1.Job{})`, RBAC for `jobs` and `configmaps`, the welcome template and
  subject, and the `Welcome`, `Schedule` and `Namespaces` template fields (ADR 0013, additive).
- Still to do: see the 2026-10-28 notes above. `.Namespaces` is empty both for "every namespace" and for Ingress annotation mode, so the
  welcome text hedges; an additive `IngressAnnotationMode` field would fix that.

**Webhook** ([ADR 0017](0017-webhook-notifier.md): Slack decided, 2026-10-30)
- `type: Slack`, with Slack's native format. One Secret per webhook in the cyclops namespace, the
  URL under the key `url`, reaching the report Pod as an env var named from the target's `name`.
- Still open: the CRD field names, who may use which Secret, a generic `type: Webhook` and Teams.
- Retries (question 4) also affect the email Jobs: `backoffLimit` is 0 today.

**Release scope: proposed, not confirmed.** A first workable `v0.1.0` that is email-only:
verify the release and install from the published chart, test real SMTP and SES, turn on
`test.yml` and `lint.yml`, settle the "controller never reads the mail password" wording, and
record the dedup decision (repeat on every run) and ADR 0016. Then the welcome email, non-ACME
diagnostics, and the webhook. If this is confirmed, ADR 0007's "v1 ships both" needs revising.

## Ground rules for this project

- No implementation until the relevant decisions are made and confirmed.
- Every decision is backed by pros/cons, not just picked.
- When there's real uncertainty (e.g. unfamiliar API behavior, unclear
  prevalence of a problem), research/read together before deciding rather
  than guessing.
