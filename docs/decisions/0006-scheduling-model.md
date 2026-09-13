# 6. Scheduling model

## Status
Decided

## Context
The workload is a once-daily batch report, not a continuous reconcile
concern. The question is whether that calls for a persistent controller
with internal scheduling, a purely external CronJob with no controller at
all, or a hybrid.

## Options considered

**A. Persistent controller with internal scheduler** (ticker or a
cron-expression-driven `RequeueAfter`)
- Pros: matches the "operator" pattern; single Deployment artifact; leaves
  the door open to future event-driven alerting on the same watch.
- Cons: idle resource cost 24/7 (informer cache of all cluster
  `Certificate` objects) for work that happens once a day; missed-run-
  after-restart handling has to be hand-built; still needs a cron-
  expression parsing dependency regardless.

**B. Stateless CronJob, no persistent controller**
- Pros: zero idle cost between runs; native `CronJob` already solves
  misfire semantics we'd otherwise reinvent (`startingDeadlineSeconds`,
  `concurrencyPolicy: Forbid/Replace`, Job retry/backoff, `kubectl get
  jobs` run history for free); simplest mental model — each run is a fresh
  List+compute+send with no cache staleness to reason about.
- Cons: doesn't fit the "reconcile a CR" idiom if there's no controller at
  all — the CRD becomes just a config object something else reads;
  precludes future event-driven alerting without adding a watcher back.

**C. Controller reconciles the CR, but manages a child CronJob** (chosen)
- The controller-runtime manager reconciles `CertReport` objects normally
  (validates spec, updates `.status`) and, on each reconcile, ensures a
  native Kubernetes `CronJob` exists/matches `spec.schedule`, owned by the
  CR via owner-reference. The CronJob's Job template runs the same binary
  in a one-shot "report" mode (list `Certificate`s, render, send, exit).
- Pros: gets Option B's operational benefits (no idle cost, native
  misfire/concurrency/retry/audit handling) while staying idiomatic
  controller-runtime (the CR is genuinely reconciled); controller can
  reflect live status onto the CR (`status.lastScheduledTime`,
  `status.observedSchedule`) by watching the CronJob/Job it owns; adding
  real-time alerting later is additive, not a rearchitecture.
- Cons: two run-modes in one binary (manager mode vs one-shot report
  mode); one more hop (CR → owned CronJob → Job) than a pure CronJob,
  though this is a well-worn pattern in other Kubernetes operators.

## Decision
**Option C.** Our controller's only job is translating a `CertReport` CR
into a native `CronJob` object; Kubernetes' own built-in CronJob
controller does the actual firing, and a one-shot Job Pod does the real
report work (list, render, send, exit). We do not write scheduling logic
ourselves.

## Illustrative reconcile flow

1. `kubectl apply` a `CertReport` with `spec.schedule: "0 8 * * *"`.
2. Our `Reconcile()` fires, finds no matching `CronJob`, creates one (with
   an owner-reference back to the CR) whose Job template runs our binary
   as `report --config=<cr-name>`.
3. Editing the CR's `spec.schedule` triggers `Reconcile()` again; it finds
   the existing `CronJob` with a stale schedule and patches it in place.
4. At the scheduled time, Kubernetes' own CronJob controller (not ours)
   creates a Job → Pod that does the one-shot list/compute/render/send
   and exits.
