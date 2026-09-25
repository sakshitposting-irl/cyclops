# 9. What puts a certificate in the report

## Status
Decided

## Context
ADR 0004 added failure diagnostics (`FailedIssuanceAttempts`, `State`,
`Reason`) to what cyclops knows about each certificate. That raises a
question it didn't answer: should a failing certificate be reported even
when its expiry is still outside the threshold (e.g. 5 failed attempts,
expiring in 60 days)?

## Options considered

**Dates only** — a certificate is reported when it is never issued, expired,
or expiring within the threshold. Failure diagnostics are shown for
certificates that are already in the report, but never cause inclusion.
- Pros: one simple, predictable rule ("the report is what's due"); the
  email doesn't fill up with transient failures that cert-manager retries
  and fixes by itself; keeps cyclops a backstop rather than a second
  alerting system for cert-manager. With cert-manager's default renewal at
  2/3 of a certificate's lifetime (~30 days before expiry for a 90-day
  cert), renewal attempts start roughly when a cert enters the default
  30-day window anyway, so a persistent failure surfaces in time.
- Cons: less early warning when renewal is configured to start well before
  the threshold and keeps failing; the reader only learns about it once the
  cert is within the threshold.

**Dates or failures** — also report any certificate with
`FailedIssuanceAttempts > 0` (or a failing `State`), regardless of expiry.
- Pros: earliest possible warning of a broken renewal.
- Cons: noisy — cert-manager retries with backoff and many failures are
  transient; the daily email would repeatedly list certificates that are
  not yet in danger. Blurs cyclops's scope into general cert-manager
  alerting.

## Decision
**Dates only.** Inclusion is decided purely by expiry (never issued,
expired, or within the threshold). Failure diagnostics are relayed for
certificates in the report, so the reader sees why a due certificate
hasn't been renewed, but they never add a certificate on their own.
