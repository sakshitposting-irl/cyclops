# 17. Webhook notifier specifics

## Status
Open. The questions are parked here to be decided later. Nothing below has
been confirmed. Where an option is marked *(leaning)*, that's only a
proposal.

## Context
ADR 0007 (revised) ships `type: Webhook` in v1 alongside `type: Email`,
with a fixed, versioned JSON payload rather than per-CR templating. Email
delivery was settled in ADRs 0014 and 0015. This ADR is for the webhook
equivalents.

## Open questions

### 1. Where is a webhook notifier configured?
Many webhook URLs are themselves the secret: for Slack incoming webhooks,
anyone with the URL can post.

- **A. Per `CertReport`, URL in a Secret** *(leaning)*
  - The spec holds `spec.notifiers[].webhook.urlSecretRef: {name}`, pointing
    at a Secret in the cyclops namespace (the same rule as ADR 0014), key
    `url`.
  - Pros: each `CertReport` can post to its own endpoint (e.g. a per-team
    channel); the URL never appears in the CR; covered by the
    `CredentialsFound` condition.
  - Cons: webhook config ends up in a different place from email, which
    is configured once per install.
- **B. Per `CertReport`, URL inline in the spec**, plus an optional
  auth-header Secret.
  - Pros: easiest to read.
  - Cons: leaks secret-bearing URLs to anyone who can
    `kubectl get certreports`.
- **C. Once per install**, in Helm values plus a Secret, like email.
  - Pros: consistent with ADR 0014.
  - Cons: every `CertReport` posts to the same endpoint, so per-team
    routing needs a relay.

### 2. Does ADR 0015 apply?
ADR 0015 says nothing is sent when there are no findings, and sends a
one-time welcome instead.

- **A. Same as email** *(leaning)*
  - Post only when there are findings, plus a one-time welcome payload
    (e.g. a `type: welcome` field) when the `CertReport` is created.
  - Pros: one rule for every notifier; the welcome checks the endpoint and
    auth up front.
- **B. Post every run**
  - Pros: webhook consumers are programs, so an empty payload per run is a
    free heartbeat (a dead-man's switch can alert when it stops).
  - Cons: behaves differently from email.
- **C. Findings only, no welcome**
  - Pros: quietest.
  - Cons: the endpoint is only tested the first time something is wrong.

### 3. How does the receiver authenticate cyclops?

- **A. An optional header from the Secret**, e.g. `Authorization: Bearer
  ...` *(leaning)*
  - Pros: works with most receivers; together with the secret URL it
    covers Slack/Teams-style webhooks and API gateways.
- **B. HMAC signature of the body** with a shared secret (like GitHub's
  `X-Hub-Signature-256`).
  - Pros: proves the request came from cyclops and wasn't altered.
  - Cons: every receiver has to implement the verification.
- **C. Both, each optional.**
  - Pros: most flexible.
  - Cons: the most to build and test.

### 4. Failures and retries
A retried Job re-runs *every* notifier, so relying on Job retries would
re-send email that was already delivered.

- **A. Retries inside the process** *(leaning)*
  - A 10s timeout per attempt.
  - Network errors, 429 and 5xx are retried up to 3 times with
    exponential backoff; other 4xx are never retried.
  - Each notifier succeeds or fails on its own (ADR 0007). The Job exits
    non-zero if any notifier failed, but its `backoffLimit` is 0, so
    nothing already delivered is re-sent.
  - This affects email too: its Job settings should match.
- **B. Rely on Job retries**
  - Pros: the simplest code.
  - Cons: every retry re-sends to all notifiers, including ones that
    already succeeded.

### 5. The exact JSON schema
ADR 0007 fixes the approach: a versioned schema, the same for every
receiver, with no templating, following Alertmanager's webhook receiver.
Still to settle:
- the field names, likely mirroring the `TemplateData`/`Row` contract from
  ADR 0013;
- the version field;
- how the welcome payload is marked, if question 2 settles on A.
