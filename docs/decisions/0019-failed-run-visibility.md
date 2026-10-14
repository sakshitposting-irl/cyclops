# 19. Failed report runs show up as a condition read from the CronJob

## Status
Decided. Settles the "failed runs need to
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
| a run is active | keep the previous value (Unknown if none) | previous reason, or `RunInProgress` |
| last scheduled run newer than last success | False | `LastRunFailed` |
| last success is the latest run | True | `LastRunSucceeded` |

False also emits a Warning Event, once per change (`warnOnTransition`).

## Consequences
- The condition clears on the next scheduled success, not when someone fixes
  the cause.
- While a run is active the previous value is kept, not replaced by Unknown.
  Otherwise a failed run's False would vanish every time the next run starts
  and anyone looking at that moment would think the problem was gone. The
  `RunInProgress` reason is only used when nothing is stored yet, which is
  rare: `NoRunsYet` is normally stored before the first run can be active.
- The message does not tell the reader where to look. The README documents
  reading the failed Job's logs, and a message naming a Job would go stale
  once that Job is pruned by the history limit.
