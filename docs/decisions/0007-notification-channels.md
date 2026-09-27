# 7. Notification channels — structure, and v1 scope

## Status
Decided. The v1 scope was revised: webhook delivery now ships in v1
alongside email (see "v1 scope" below). The list structure and the webhook
payload decision are unchanged.

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

## v1 scope: email and webhook

**Originally decided:** email only in v1, webhook deferred to v2. Webhook
support was real scope but not part of the originally stated goal (SES/SMTP
email was explicit from the start; webhook came up later in design
discussion). Only `type: Email` would be implemented in v1, with
`type: Webhook` reserved.

**Revised:** v1 ships **both `type: Email` and `type: Webhook`**. Moving it
forward is cheap:
- the payload shape was already decided (below), so the webhook contract is
  known;
- the list structure makes it additive: a second `type` case, with no
  schema change to existing `Email` notifiers.

The list shape stands for the same reason as before: adding further
channel types later is a new `type` case in an existing array, not a
breaking CRD change.

Webhook specifics are not decided here; they are parked in
[ADR 0017](0017-webhook-notifier.md) (open). They cover
where the URL and any auth credentials live (per
`CertReport`, or once per install like email in ADR 0014), whether ADR
0015's "nothing sent when empty" and welcome message apply to webhooks,
retries and timeouts, and the exact versioned JSON schema.

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
