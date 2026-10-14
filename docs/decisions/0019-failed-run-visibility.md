# 19. Failed report runs show up as a condition read from the CronJob

## Status
Decided: the approach. Open: the TODOs below. Settles the "failed runs need to
show up somewhere" part of the Observability open item. Metrics, logging and
a `kubectl get` health column are still open.

## Context
ADR 0015 sends no email on a healthy day, so a report Job that fails (bad
credentials, mail server down, Pod cannot start) produces no email either.
Silence then looks identical to "all certificates are fine". A tool whose job
is not to miss things must not fail silently.

## Options considered

**A. Condition from the CronJob's status** (chosen)
- The controller already owns and fetches the CronJob. Its status has
  `lastScheduleTime`, `lastSuccessfulTime` and `active`. A run that was
  scheduled after the last success, with nothing active, failed.
- Pros: no new watch, RBAC, informer or cache entry; the existing
  `Owns(CronJob)` already wakes the reconciler when CronJob status changes.
- Cons: says that a run failed, not why; the reason is in the Job's Pod logs.
  A manually created Job does not touch CronJob status, so it is not counted.

**B. Watch owned Jobs**
- Pros: can carry the failure reason from the Job and Pod.
- Cons: a Job informer, RBAC and cache entry. Revisit when the welcome email
  (ADR 0015) adds the Job watch anyway.

**C. Warning Event only**
- Cons: Events expire after about an hour, so `kubectl get certreport` cannot
  show the problem later.

**D. Document the gap**
- Cons: ships a known silent failure.

## Decision
Option A. New condition `ReportSucceeding`:

| State of the CronJob | Status | Reason |
|---|---|---|
| suspended | Unknown | `Suspended` |
| never scheduled | Unknown | `NoRunsYet` |
| a run is active | keep the previous value (Unknown if none) | `RunInProgress` |
| last scheduled run newer than last success | False | `LastRunFailed` |
| last success is the latest run | True | `LastRunSucceeded` |

False also emits a Warning Event, once per change (`warnOnTransition`).

## Consequences
- The condition clears on the next scheduled success, not when someone fixes
  the cause.
- TODO(you): is "keep the previous value while a run is active" the right call,
  or should an active run show Unknown?
- TODO(you): should the message point at the command to read the logs, e.g.
  `kubectl -n cyclops-system logs job/<name>`? That needs the Job name, which
  CronJob status does carry in `active` but not for finished runs.
