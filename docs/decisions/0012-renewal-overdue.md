# 12. Report by overdue renewal, not a fixed threshold

## Status
Decided. Supersedes the threshold part of ADR 0009; the rest of 0009 (dates
decide inclusion, failure diagnostics never do) stands.

## Context
ADR 0009 reports a certificate when it is never issued, expired, or expires
within a fixed threshold (default 30 days) set on the `CertReport`. One
threshold for every certificate fits poorly: certificate lifetimes range
from hours to years, and cert-manager already works out, per certificate,
when renewal should happen.

cert-manager sets `status.renewalTime` on each issued Certificate (from
`renewBefore`, `renewBeforePercentage`, or by default 2/3 of the way through
its lifetime) and starts renewing at that moment. A successful renewal
moves both `notAfter` and `renewalTime` forward. So a `renewalTime` that is
in the past means cert-manager was due to renew the certificate and hasn't
succeeded, which is exactly the failure cyclops exists to catch.

## Options considered

**A. Fixed threshold (ADR 0009 as written)**
- Pros: simple; one knob.
- Cons: the same window for a 24-hour certificate and a 1-year one; a
  certificate renewing normally inside the window is reported anyway.

**B. Past `renewalTime`, replacing the threshold**
- Pros: per certificate, with no configuration; reports only certificates
  whose renewal has actually failed, so healthy certificates mid-lifetime
  never appear.
- Cons: a certificate configured with a very small `renewBefore` (e.g. 1h)
  only becomes overdue shortly before it expires, leaving little warning.

**C. Both: past `renewalTime` or within a threshold**
- Pros: guards against very small `renewBefore` values.
- Cons: two rules and a knob to explain, and the threshold brings back
  reports for healthy certificates.

## Decision
**Option B.** A certificate is reported when it is:

| Kind | Rule |
|---|---|
| `NeverIssued` | no `notAfter` |
| `Expired` | `notAfter` is in the past |
| `RenewalOverdue` | `renewalTime` is more than **1 hour** in the past |

checked in that order, first match wins. `RenewalOverdue` replaces
`ExpiringSoon`, and the `CertReport` has no threshold field.

**Grace period.** cert-manager starts renewing at `renewalTime`, so a
certificate is briefly past it while a normal renewal runs (seconds for
self-signed, minutes for ACME). A fixed 1-hour grace keeps those out of the
report. It is a constant in code, not a setting: any real renewal finishes
well within it, and an hour's delay doesn't matter for a daily report.

**No `renewalTime`.** An issued certificate without one only matches
`NeverIssued` or `Expired`.

**Accepted risk.** A very small `renewBefore` gives little warning before
expiry. That's the owner's explicit choice in their Certificate, and
`Expired` still catches the worst case.

Ordering stays as in ADR 0009: most urgent first by `notAfter`, then
namespace, then name.
