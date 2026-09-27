# 15. Email only when something is wrong, plus a one-time welcome

## Status
Decided. Settles the "is an email sent when there's nothing to report"
part of the state & dedup open item. How to avoid re-alerting on the same
certificate every day is still open.

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
- *Explanation only.* Simpler; findings wait for the first scheduled run.
- *Explanation plus the current findings* (chosen). The first email is
  immediately useful, and says either what's wrong now or that everything
  is healthy.

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
     `ttlSecondsAfterFinished`.
   - Its name includes a hash of the `CertReport`'s `generation` and the
     credentials Secret's `resourceVersion`. The controller creates the
     Job only if none with the current hash exists.
3. **Welcome mode** runs the same list/evaluate/render as a scheduled run,
   with two differences:
   - It always sends, even when there are no findings.
   - The template data sets `Welcome: true` along with the schedule and
     namespaces covered. These are additive fields under ADR 0013.

   The default template adds a short explanation:
   - cyclops only emails when a certificate is never issued, expired, or
     more than an hour past its renewal time;
   - the schedule;
   - the namespaces covered.

   Custom templates (ADR 0005) that ignore `.Welcome` still render the
   findings.
4. **Recording the result.** The controller watches the Jobs it owns.
   - On success, it writes `welcomeSentAt` to the ConfigMap and sets
     `WelcomeSent=True` on the `CertReport`. The report Job itself stays
     read-only.
   - On failure (after the Job's `backoffLimit`), it sets
     `WelcomeSent=False` with reason `JobFailed` and emits a Warning Event.
5. **Retrying after a fix.** Editing the `CertReport` or its credentials
   Secret changes the hash, so the controller creates a new welcome Job.
   A failed Job with an unchanged hash isn't retried. The controller
   already watches that Secret for `CredentialsFound` (ADR 0014).

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
