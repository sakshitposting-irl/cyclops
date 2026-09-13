# 4. Failure diagnostics depth

## Status
Decided

## Context
cert-manager surfaces renewal-failure information at different depths of
its own resource graph: `Certificate` → `CertificateRequest` → `Order` →
`Challenge`. The question is how deep to walk this chain, and how much of
what we find to expose in the report.

## What's actually available (from cert-manager's API types)

- `Certificate.status`: `Conditions[Ready]`/`Conditions[Issuing]` (types
  are a fixed enum, but `Reason`/`Message` text is explicitly
  implementation-specific free text, not stable); `NotAfter`, `NotBefore`,
  `RenewalTime`; **`FailedIssuanceAttempts`** — a numeric counter of
  continuous failed attempts, usable with no traversal at all.
- `Order.status`: `State` — a real, stable enum
  (`pending|processing|valid|ready|invalid|expired|errored`); `Reason`
  (free text); `FailureTime`.
- `Challenge.status`: same `State` enum type; `Processing`/`Presented`/
  `PresentedAt`; `Reason` (free text) — this is where a specific message
  like "self-check GET request... redirected" vs "...connection refused"
  actually lives, but it is documented as human-readable text with no
  stability guarantee across cert-manager versions or ACME
  servers/solvers.

## Options considered

**Structured fields only** (`State` enum, `FailedIssuanceAttempts`,
`RenewalTime`/`NotAfter`)
- Pros: fully stable across cert-manager versions, no free-text parsing.
- Cons: doesn't tell the reader *why* it's failing beyond a coarse state.

**Structured fields + raw `Reason` text passthrough (verbatim, undisplayed
categorization)**
- Pros: gives the human reader cert-manager's own diagnostic string
  as-is, which research confirmed is often specific and actionable (e.g.
  distinguishing an HTTP-01 redirect problem from an unreachable challenge
  path — a well-documented, recurring real-world failure class, see
  cert-manager/cert-manager#419, #3238). Low fragility since we're not
  parsing/interpreting the text, only relaying it.
- Cons: none significant — we accept the text as opaque.

**Structured fields + our own categorization** (regex/pattern-matching the
free-text `Reason` into buckets like "redirect" vs "unreachable" vs
"rate-limited")
- Pros: most actionable at a glance ("Likely cause: HTTP-01 redirect
  interference").
- Cons: brittle — message wording varies across cert-manager versions and
  ACME server/solver implementations (confirmed by research showing
  different phrasing across DigitalOcean vs generic Let's Encrypt
  scenarios); would silently miscategorize or stop categorizing on upstream
  wording changes with no compile-time signal that it broke. A maintenance
  trap for a project explicitly meant not to be rushed.

## Other real failure classes found during research (informational)
- Let's Encrypt rate limiting (50 certs/domain/week) exhausted by a
  misconfigured cluster looping Certificate create/delete.
- `CertificateRequest`/`Order` ownership conflicts ("found Order resource
  not owned by this CertificateRequest").
- Apps that read TLS files once at startup won't notice a successful
  renewal without a pod restart — a distinct failure class from
  cert-manager renewal failure, out of scope for this controller.

## Decision
Report **structured fields** (`State`, `FailedIssuanceAttempts`,
`RenewalTime`/`NotAfter`) **plus the raw `Reason` text passthrough,
verbatim**, from the `Order`/`Challenge` chain. No message-based
categorization layer.
