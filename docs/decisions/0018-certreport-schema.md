# 18. `CertReport` schema: spec fields, validation and status

## Status
Partly decided. Questions 1, 3, 5, 6 and 7 are decided below (each has a
**Decision** line); question 8 is deferred; questions 2, 4 and 9, and the
template sub-questions under 7, are still open. Where an option in an open
question is marked *(leaning)*, that's only a proposal. The "Already decided"
section only collects what earlier ADRs settled; it doesn't reopen them.
`api/v1alpha1/certreport_types.go` waits for the open ones.

## Context
`CertReportSpec` is still the kubebuilder placeholder, with a `TODO` listing
`namespaces filter, thresholdDays, schedule, notifiers,
emailTemplateConfigMapRef`. Those fields come from ADRs written one at a
time, so the schema was never looked at as a whole. Several things are
unsettled: what validation the CRD itself enforces, how the optional
`CronJob` settings are exposed, and what goes in `status`. The reconciler
(ADR 0006), the template loader (ADR 0005) and the cluster tests all depend
on the answers.

## Already decided (not reopened here)

| Field | Shape | ADR |
|---|---|---|
| `spec.schedule` | cron expression, copied to the owned `CronJob` | 6 |
| `spec.namespaces` | list of namespace names; omitted or empty = all | 10 |
| `spec.notifiers[]` | typed list: `type` plus a block named after it | 7 |
| `spec.notifiers[].email` | `from` and `to` only; provider, host and credentials are install-level, not in the CR | 14 |
| `spec.emailTemplateConfigMapRef` | `{name, key}`; omitted = built-in template | 5 |
| `status.conditions` | `NamespacesFound` (11), `CredentialsFound` (14), `WelcomeSent` (15) | 11, 14, 15 |
| scope | cluster-scoped, single Kind | 2 |
| **no** threshold field | inclusion is by dates only, with no day count | 9, 12 |

Also fixed, outside the ADRs: the email `from` and `to` are **bare
addresses** (`cyclops@example.com`), with no display names. Both senders
use the bare address (`SES.Send`; SMTP uses the same value in its
envelope).

## Corrections to earlier text
- The placeholder `TODO` lists **`thresholdDays`**. ADR 0012 removed the
  threshold, so the field must not be added. The `TODO` goes.
- ADR 0005's example YAML uses `apiVersion: cyclops.io/v1alpha1` and a
  template that prints `{{ .ThresholdDays }}`. The group is
  `cyclops.sakshitposting-irl.github.io/v1alpha1`, and ADR 0013's
  `TemplateData` has no threshold. The example is illustrative and stale,
  not a decision.

## Open questions

### 1. Schedule: required or defaulted, and time zone?
**Decision: option B.** `schedule` defaults to `0 8 * * *`, UTC only. There
is no `timeZone` field. The report isn't time-critical, so the simpler
schema wins. Adding an optional `timeZone` later is compatible.

The goal is a daily report. `CronJob` has its own `spec.timeZone`; without it
the schedule runs in the controller manager's zone, which is UTC in practice
but isn't something a user can see.

- **A. `schedule` required, plus optional `timeZone` passed to the `CronJob`**
  *(leaning)*
  - Pros: no surprise zone; a plain pass-through of a Kubernetes field;
    `8:00 Europe/London` stays at 8:00 across daylight saving time.
  - Cons: one more field; an invalid zone name is only caught when the
    `CronJob` is created (see question 2).
- **B. `schedule` with a default of `0 8 * * *`, UTC only**
  - Pros: the smallest working `CertReport` is just `notifiers`.
  - Cons: a hidden default decides when mail arrives; no way to ask for the
    local time without changing the schema later.
- **C. `schedule` required, UTC only**
  - Pros: simplest.
  - Cons: same as B for time zones.

### 2. How is a bad schedule caught?
**Open.** The API server validates the cron expression itself when the
controller creates the `CronJob`, so option C is the floor. The remaining
question is whether to add anything on top: only a status condition carrying
the API server's message, or also a loose CRD check so obvious junk fails at
`kubectl apply`. The `CertReport` is accepted either way; the difference is
how soon the user finds out.

Cron syntax can't be written as a readable regex (ranges, steps, names,
`@daily`).

- **A. Loose CRD check (`MinLength`, five fields or an `@` macro), exact check
  in the controller** *(leaning)*
  - The controller sets a condition (a new `ScheduleValid`, or reuses
    `Available` with a reason) when the `CronJob` create or update fails.
  - Pros: obvious typos fail at `kubectl apply`; the API server's own parser
    has the final word, so we don't carry a copy of it.
  - Cons: a subtle error is only visible in status.
- **B. Parse it in Go in the controller before creating the `CronJob`**
  - Pros: precise errors.
  - Cons: another dependency, and it can disagree with the API server.
- **C. No validation at all; surface the `CronJob` create error**
  - Pros: least code.
  - Cons: a poor error experience.

### 3. Which `CronJob` settings does the CR expose?
**Decision: option C.** `suspend`, `startingDeadlineSeconds`, the history
limits and `backoffLimit` are optional spec fields. Each has a default, and
users may override them. The defaults are chosen when the types are written.
The risk noted under C stands: a retried email Job can send twice
(ADR 0015), so the `backoffLimit` default should be low and documented.

The controller sets everything else itself.

- **A. None; fixed values** (`concurrencyPolicy: Forbid`, small history
  limits, a fixed `startingDeadlineSeconds`, a small Job `backoffLimit`)
  - Pros: smallest schema; no way to misconfigure a run.
  - Cons: there's no way to pause a report except deleting it.
- **B. Only `suspend`** *(leaning)*
  - Pros: pausing during maintenance or an incident is the one thing people
    reach for; one boolean.
  - Cons: a second source of truth if someone edits the `CronJob` directly
    (the controller's patch wins).
- **C. `suspend` plus `startingDeadlineSeconds`, history limits and
  `backoffLimit`**
  - Pros: flexible.
  - Cons: more surface to document and test; ADR 0015 makes retries matter
    (a retried email Job can send twice), so exposing them early is risky.

### 4. Validation of the notifier list
**Open.**

- **Discriminated union.** `type` is an enum (`Email`; `Webhook` joins with
  ADR 0017). A CEL rule checks that exactly the block named by `type` is set,
  so `type: Email` with no `email:` fails at apply, not in the Job.
- **`minItems: 1`** *(leaning)*: with no notifier a report does nothing.
- **Several `Email` entries are allowed** (ADR 0007 says the list is for
  that), so there's no uniqueness rule on `type`.
- **List identity.** Choose how entries are identified:
  - **A. Plain atomic list** *(leaning)*: simplest; any edit replaces the
    list.
  - **B. A `name` per entry, `listType=map`**: stable identity for
    per-notifier status, events and the welcome state (ADR 0015 records it
    per `CertReport`, ADR 0017 asks whether webhooks get their own). Cons: a
    required field with no use until those exist. Adding it later as
    optional is compatible, so this can wait.
- **Webhook block.** Leave it out until ADR 0017 is decided *(leaning)*.
  Adding an enum value and an optional block later is additive.

### 5. Validation of email addresses
**Decision: option C.** The CRD has no address rule. `parse` in report mode
is the only validator. Because "bare address only" is now enforced there,
**`parse` is to reject display names** (`Cyclops <a@b.com>`), so a name fails
the report with a clear error and SES and SMTP can't treat it differently. A
bad address therefore surfaces in a failed Job and not at `kubectl apply`,
which was the stated cost of this option. Follow-up code change, not done
yet: tighten `parse` and update `TestParseValid` and the display-name cases in
`TestParseAddress`.

The options below are kept as the record of what was considered.

The addresses reach mail headers (ADR 0014's CR/LF rules), so the CRD could
reject bad ones at apply.

- **A. `+kubebuilder:validation:Pattern` on `from` and on each `to` item**
  *(leaning)*
  - For example `^[^@\s<>,;"]+@[^@\s<>,;"]+$`, which rejects display names,
    commas and whitespace (including CR/LF), plus `minItems: 1` on `to`.
  - Pros: fails at `kubectl apply`; no webhook; keeps "bare address" a
    property of the schema.
  - Cons: a loose pattern isn't RFC 5322, so `parse` stays the real check.
- **B. `format: email`**
  - Pros: no regex to maintain.
  - Cons: I haven't confirmed that the API server enforces it for CRDs
    (it may accept the format name and ignore it). Check before relying on
    it.
- **C. Validate only in report mode (`parse`)**
  - Cons: a bad address surfaces at 08:00 in a failed Job, not when applied.

Sub-question still open: a recipient cap. SES limits the destination count
per message; the exact limit should be confirmed before picking a number.
With no CRD rule, the cap would live in `parse`.

### 6. Validation of `namespaces`
**Decision: yes.** Each item must match a DNS-1123 label (max 63 characters),
the list is `listType=set`, and it has a modest `maxItems` (the number is
picked when the types are written).

Existence stays in the controller (ADR 0011). The CRD can still check the
*form*.

- Each item matches a DNS-1123 label: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`,
  `maxLength: 63`.
- `listType=set` (duplicates are meaningless) and a modest `maxItems`.
- Leaning: yes. It's cheap, and a typo like `Prod` (capital) fails at apply
  instead of showing up later as `NamespacesFound=False`.

### 7. Where does the template ConfigMap live?
**Decision: option A.** The reference resolves to the cyclops namespace, like
the credential Secrets (ADR 0014). There is no `namespace` field, and the
controller needs no cluster-wide ConfigMap access.

The reference is `{name, key}`, and the `CertReport` is cluster-scoped, so
there's no namespace to infer.

- **A. Implicitly the cyclops namespace** *(leaning)*
  - Same rule as the credential Secrets (ADR 0014); the controller's
    namespace-scoped Role already covers reading it.
  - Pros: no cluster-wide ConfigMap RBAC; one place to look.
  - Cons: users must create the ConfigMap in that namespace.
- **B. An explicit `namespace` field**
  - Pros: more flexible.
  - Cons: needs `get`/`watch` on ConfigMaps cluster-wide, which widens RBAC
    for a convenience.

Sub-questions, still open:
- Is `key` required, or defaulted (for example to `template.html`)? Leaning:
  required, so the reference is explicit.
- Should the controller parse the template and set a condition (for example
  `TemplateValid`) so a broken template shows up before 08:00? ADR 0005 says
  the controller watches the ConfigMap; this is a small addition to that.

### 8. What does `status` contain?
**Deferred.** Not decided yet; to be planned together with the observability
item in the ADR index. Nothing below is confirmed. Until then `status` keeps
the scaffold's `conditions` list, which already holds the three condition
types above.

- **A. Conditions plus `observedGeneration`** *(leaning)*
  - `NamespacesFound`, `CredentialsFound`, `WelcomeSent`, and whichever of
    `ScheduleValid` / `TemplateValid` are accepted above. Each carries
    `observedGeneration`, which is standard for conditions.
  - Pros: all of it is rebuildable from other objects, which ADR 0015
    wants for status.
  - Cons: no at-a-glance view of the last run.
- **B. A + mirrored run fields** (`lastScheduledTime`, `lastSuccessfulTime`
  copied from the `CronJob`)
  - ADR 0006 mentions mirroring `lastScheduledTime`.
  - Pros: `kubectl get certreport` shows that runs are happening, which
    matters because healthy days send no email (ADR 0015).
  - Cons: overlaps the open observability item in the ADR index (a failed
    run is invisible). That item should decide the run-health shape first;
    these fields can be added later without breaking anything.

Leaning: A now, B when the observability item is decided.

### 9. Printer columns and short name
**Open.**

Small, but fixed once people script against `kubectl get`. Leaning: columns
for `Schedule`, a suspended flag (if question 3 says B or C), the `Ready`-like
summary condition, and `Age`. No short name.

## Illustrative shape (not confirmed)

```yaml
apiVersion: cyclops.sakshitposting-irl.github.io/v1alpha1
kind: CertReport
metadata:
  name: cluster-cert-report
spec:
  schedule: "0 8 * * *"              # default; UTC only (question 1)
  suspend: false                     # question 3; also deadline,
                                     # history limits, backoffLimit
  namespaces: [prod, staging]        # omitted or empty = all
  emailTemplateConfigMapRef:         # omitted = built-in template
    name: cert-report-template       # in the cyclops namespace (question 7)
    key: report.html.tmpl
  notifiers:
    - type: Email
      email:
        from: cyclops@example.com    # bare address only; parse rejects names
        to: [platform@example.com]
```

## Next steps
- Settle questions 2 and 4, the template sub-questions under 7, and 9;
  question 8 comes with the observability item.
- Tighten `parse` to reject display names (question 5).
- Then write the types.

## Consequences once decided
- `make manifests generate` regenerates the CRD and DeepCopy code.
- The sample in `config/samples/` is filled in (its `TODO` goes).
- The reconciler, the template loader and the report-mode entry point can
  start against a fixed schema.
- The CRD tests in `internal/controller/` can use real fields instead of the
  scaffold placeholders.
