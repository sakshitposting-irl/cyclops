# 22. No cross-day dedup: a certificate is reported on every run

## Status
Decided (2026-10-30). Closes the part of the "state & dedup" item that
[ADR 0015](0015-email-policy.md) left open.

## Context
ADR 0015 sends nothing on a healthy run. The question left was whether a
certificate that was reported yesterday and is still in trouble should be
reported again today, or kept quiet until something changes.

## Options considered

**A. Report it on every run** *(chosen)*
- Pros: the report is stateless: a run needs only the current certificates,
  so nothing can go wrong with stored state (lost, stale or wrong). A
  certificate that stays broken keeps showing up, so a long-running problem
  can't be forgotten. It fits cyclops's job as a backstop for cert-manager's
  own renewal.
- Cons: someone who already knows about a broken certificate gets the same
  entry daily until it is fixed.

**B. Report only what is new since the last run**
- Pros: quieter.
- Cons: needs per-certificate state to compare against, and a failure there
  (state lost or wrong) would hide a certificate. A certificate that is
  still broken on day 2 goes quiet, which is when it matters most.

## Decision
Option A. Every scheduled run reports every certificate that meets the
inclusion rule of ADR 0009 and 0012, whether or not it was reported before.
There is no per-certificate state. The per-`CertReport` state ConfigMap of
ADR 0015 stays what it is: the record that the welcome email was sent.

The reasoning, as decided: there is nothing to gain from staying silent on
day 2 about something that is still broken.

## Consequences
- `Evaluate` returns the same findings for the same certificates, run after
  run.
- Someone who finds the repeats noisy can narrow `spec.namespaces` or run
  the schedule less often.
