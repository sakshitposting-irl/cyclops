# 7. Notification channels — structure, and v1 scope

## Status
Decided

## Context
The project should eventually support both email and webhook delivery.
Two questions: how do multiple channels coexist in the CRD, and what's
actually in scope for v1.

## Structure: typed notifier list vs fixed top-level fields

Precedent: Prometheus Alertmanager solved this exact problem (email,
webhook, Slack, PagerDuty as pluggable "receivers") with a list of typed
receiver configs, each independently sent to.

**List of typed notifiers** (`spec.notifiers: [{type: Email, email: {...}},
{type: Webhook, webhook: {...}}]`)
- Pros: extensible — a new channel later is just a new `type` case, no
  schema-breaking change; each entry independently configured and sent,
  so one channel failing doesn't block another; matches the Alertmanager
  precedent.
- Cons: slightly more nested YAML than flat fields.

**Fixed top-level fields** (`spec.email: {...}`, `spec.webhook: {...}`)
- Pros: flatter for exactly two channels.
- Cons: doesn't generalize to a third channel or multiple instances of the
  same channel type naturally.

Decision: **typed notifier list.**

## v1 scope: email only, webhook deferred to v2

Webhook support is real scope, but not part of the originally stated goal
(SES/SMTP email was explicit from the start; webhook came up later in
design discussion). Decision: **implement only `type: Email` in v1**;
`type: Webhook` is reserved for v2.

Follow-on decision: since the list shape was already agreed as the right
long-term structure, we adopt the `notifiers: [...]` list shape in the
CRD **now**, even though only `Email` is implemented, rather than shipping
a simpler singular `spec.email: {...}` field for v1. This avoids a
breaking CRD schema change (and associated version bump / conversion
webhook) when webhook support is added in v2 — the addition becomes purely
a new `type` case in an already-existing array.

## Webhook payload shape (decided ahead of implementation, for the CRD
contract's sake)

Fixed, versioned JSON schema, not per-CR templated — mirroring
Alertmanager's own webhook receiver, which POSTs a fixed schema and leaves
reshaping (e.g. for Slack) to a downstream relay/adapter rather than
letting the sender template arbitrary output. Rationale: webhook consumers
are programs, not humans, and want a stable contract to code against, not
a moving target; also avoids adding a second templating engine (`text/
template`, with JSON-specific escaping concerns) alongside the `html/
template` path already used for email (ADR 0005).
