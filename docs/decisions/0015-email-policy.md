# 15. Email only when something is wrong, plus a one-time welcome

## Status
Decided. Settles the "is an email sent when there's nothing to report"
part of the state & dedup open item. How to avoid re-alerting on the same
certificate every day is still open.

Welcome email built (2026-10-27); not yet run on a real cluster.

Amended: the welcome email carries no findings. They arrive in the
scheduled run's own email, a separate message even if both go out at the
same time.

## Context
A report run can find no certificate that is never issued, expired, or
overdue for renewal (ADR 0012). The renderer already handles this case
(subject `[cyclops] All certificates healthy`, a one-line body), but
whether that email should be sent was never decided.

## Options considered: empty scheduled runs

**A. Always send**
- Pros: simplest; the daily email doubles as proof that cyclops is alive.
- Cons: daily noise that people learn to ignore or filter, which can hide
  the day it matters.

**B. Configurable per `CertReport`, default send**
- Pros: a heartbeat by default, with an opt-out.
- Cons: one more spec field and two behaviours to test.

**C. Never send when empty**
- Pros: quietest; every email that arrives needs action, so none are
  trained into being ignored.
- Cons: silence is ambiguous. It could mean everything is healthy, or
  that the Job is failing, the credentials are broken, or the CronJob is
  gone. It also means recipients may never hear from cyclops at all, and
  won't know it exists or what silence means.

## Options considered: the welcome email

C's second con is addressed by a one-time welcome email that explains the
policy. Choices within that:

**When it's sent**
- *Once per Helm install* (a post-install hook). Rejected: recipients are
  set in a `CertReport`, and at install time there usually isn't one yet.
- *When each `CertReport` is created* (chosen). With the usual single
  cluster-wide `CertReport` (ADR 0002) this is once per install. A second
  report with its own recipients gets its own welcome email.
- *Also when recipients change.* Needs per-address state; not needed yet.

**What it contains**
- *Explanation only* (chosen). Findings arrive in the scheduled run's own
  email, so the welcome never repeats them and the two messages stay
  independent, even if both go out on the same day.
- *Explanation plus the current findings* (the original choice, dropped).
  The first email would have been immediately useful, but a welcome sent
  the same day as a scheduled run would list the same findings twice.

**Who sends it**
- *The controller directly.* Rejected: the controller would need the mail
  credentials, undoing ADR 0014's "only the report Pod gets credentials".
- *The first scheduled run.* Rejected: the welcome waits up to a day, and
  the report Job would need write access to record that it sent it.
- *A one-off Job created by the controller from the CronJob's template*
  (chosen), the same as `kubectl create job --from=cronjob`.

**Where "already sent" is remembered**
- *`CertReport` status.* Idiomatic, but status is meant to be rebuildable
  from what's in the cluster, and "a welcome was sent" can't be. Backup
  and restore, or GitOps recreating the object, would drop it and send
  the welcome again.
- *A small ConfigMap per `CertReport`* (chosen). It's owned by the
  `CertReport` through an owner reference, so deleting the report deletes
  the state, and a re-created report is welcomed again.

## Decision

### Scheduled runs: never send when empty (C)
Report mode sends only when the report has at least one finding. An empty
run logs that it found nothing and exits successfully, without contacting
SES or SMTP. There's no spec field for this. If a heartbeat is wanted
later, an optional field defaulting to "don't send" can be added without
breaking anything.

### Welcome email: once per `CertReport`
1. **State.** For each `CertReport`, the controller keeps a ConfigMap
   `cyclops-state-<certreport-name>` in its own namespace, owned by the
   `CertReport`. When the welcome email has been delivered, it holds
   `welcomeSentAt`.
2. **Sending.** If the ConfigMap has no `welcomeSentAt`, the controller
   creates a one-off Job from the CronJob's Job template.
   - The Job runs report mode with `--welcome`.
   - The Job is owned by the `CertReport` and has a
     `ttlSecondsAfterFinished` of 24 hours.
   - It runs once: it inherits the CronJob's `backoffLimit` (0 today), so
     one failed attempt is a failed Job. Retries for all email Jobs are
     part of the open webhook/retry question (ADR 0017).
   - Its name includes a hash of the `CertReport`'s `generation` and the
     credentials Secret's `resourceVersion`. The controller creates the
     Job only if none with the current hash exists.
3. **Welcome mode** runs the same list/evaluate/render as a scheduled run,
   with two differences:
   - It always sends, even when there are no findings.
   - The template data sets `Welcome: true` along with the schedule and
     namespaces covered. These are additive fields under ADR 0013.
     `Findings` is still filled in truthfully, so a custom template (ADR
     0005) that ignores `.Welcome` never claims "all healthy" when something
     is wrong.

   The welcome email has its own built-in template (`default-welcome.html.tmpl`),
   separate from the report's, so the two can be restyled independently. It
   shows only a short explanation, no findings:
   - cyclops only emails when a certificate is never issued, expired, or
     more than an hour past its renewal time;
   - the schedule;
   - the namespaces covered.

   A user overrides it with a second key, `welcome.html`, in the same
   ConfigMap that `emailTemplateConfigMapRef` (or the chart-wide default,
   ADR 0005 amendment) already points at. A ConfigMap without that key, or no
   reference at all, uses the built-in welcome template. The alternative, a
   separate `CertReport` field and chart value, was rejected: it adds API
   surface (ADR 0018) for something one extra key does. The `TemplateValid`
   condition also parses `welcome.html` when it is present.

   The subject is fixed text, without counts (`[cyclops] Welcome! Let's
   get you started`). Findings are only in the scheduled run's email.
4. **Recording the result.** The controller watches the Jobs it owns.
   - On success, it writes `welcomeSentAt` to the ConfigMap and sets
     `WelcomeSent=True` on the `CertReport`. The report Job itself stays
     read-only.
   - On failure (after the Job's `backoffLimit`), it sets
     `WelcomeSent=False` with reason `JobFailed` and emits a Warning Event.
5. **Retrying after a fix.** Editing the `CertReport` or its credentials
   Secret changes the hash, so the controller creates a new welcome Job.
   A failed Job with an unchanged hash isn't retried, even after the Job's
   TTL has deleted it: the controller keeps `WelcomeSent=False` (reason
   `JobFailed`), whose message names the failed Job, and does not create
   that Job again. While the credentials Secret is missing no Job is
   created and `WelcomeSent` is `Unknown`. The controller
   already watches that Secret for `CredentialsFound` (ADR 0014).

## Known limits
- **At-least-once delivery.** A Job that sends and then fails can send
  twice; deleting the state ConfigMap by hand sends the welcome again.
- **Names.** The state ConfigMap `cyclops-state-<name>` is not a valid
  name if the `CertReport`'s name is over 239 characters. The Job name is
  cut and hashed to fit 63 characters.
- **`.Namespaces`** is empty both for "every namespace" and for Ingress
  annotation mode, so the built-in welcome text covers both.

## RBAC changes (relative to ADR 0014)
Controller, a Role in its own namespace:
- `get`, `list`, `watch`, `create` on `jobs` (`batch`);
- `get`, `create`, `update` on `configmaps`.

## Consequences
- **Credential problems are caught when the report is created.** The
  welcome email goes through the same provider and credentials, so a wrong
  password or IAM role fails right away, visible as `WelcomeSent=False`.
  Credentials that break *later* (rotation, a revoked role) still stay
  hidden until the first real finding, because empty scheduled runs never
  contact the mail server.
- **A failed scheduled run looks the same as a healthy cluster** from the
  inbox. Failed runs have to show up elsewhere: Job status, and ideally a
  condition on the `CertReport` driven by the Jobs it owns. This belongs to
  the observability open item.
- **The per-`CertReport` state ConfigMap** is where dedup state (the
  remaining open part of state & dedup) could also live. That's for that
  decision to settle.
