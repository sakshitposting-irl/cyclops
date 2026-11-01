# 17. Webhook notifier specifics

## Status
Partly decided (2026-10-30; the CRD shape and the welcome on a Secret change
added 2026-11-02). The Slack notifier is decided, in "Decided: the
Slack notifier" below. The questions under "Open questions" keep their
original options as a record, and each says whether Slack settled it. What
is still open is under "Still open". Where an option is marked *(leaning)*,
that's only a proposal.

## Context
ADR 0007 (revised) ships `type: Webhook` in v1 alongside `type: Email`,
with a fixed, versioned JSON payload rather than per-CR templating. Email
delivery was settled in ADRs 0014 and 0015. This ADR is for the webhook
equivalents. The first webhook channel is Slack, as a `type: Slack` of its
own with Slack's native payload format (ADR 0007 is amended to match).

## Decided: the Slack notifier

- **Its own type, with Slack's format** (question 5, option B for Slack).
  `type: Slack` is a typed notifier in the ADR 0007 list. cyclops posts
  Slack's native Block Kit payload, so an incoming webhook URL works with
  no relay. Teams and a generic JSON webhook are not decided here.
- **Several webhooks per notifier.** A Slack notifier has a list of
  targets, and a `CertReport` sends to all of them. Targets and
  `CertReport`s are many-to-many: a `CertReport` can list any number, and
  several can reference the same one. Each target has a `name`, used as its
  label in conditions, logs and errors.
- **Where the URL lives** (question 1, option A). In a Secret in the
  namespace cyclops is installed in, the same rule as ADR 0014: a
  `CertReport` has no namespace, and a Pod can only use Secrets from its
  own. **One Secret per webhook**, the URL under the key `url`. The CR holds
  only the Secret's name, never the URL, so the URL is not in the spec, in
  git, in `last-applied-configuration` or in audit logs.
  - Why one per webhook: a report's Pod receives only the URLs its CR
    lists, and one URL can be rotated or revoked without touching the
    others. Rejected: the URL inline in the spec (readable by anyone who
    can read `CertReport`s); one URL per install (every report to the same
    channel); one Secret holding several URLs (a report would receive more
    than it names, or the controller must wire each key separately).
  - This is not an access control. Anyone who can create a `CertReport`
    can name any such Secret, so teams are not isolated from each other's
    channels. See "Still open".
  - Adding a channel needs write access to the cyclops namespace.
- **How the URL reaches the report Pod.** As email's credentials do
  (ADR 0014): the controller adds an environment variable with a
  `secretKeyRef` to the CronJob's Pod template, and the kubelet fills it
  in. The controller checks that each Secret exists, which needs `get` on
  Secrets in its own namespace, as it has today. The report ServiceAccount
  gets no Secret access.
- **Environment variable names come from the target's `name`**: the prefix
  `CYCLOPS_SLACK_`, then the name in upper case with `-` turned into `_`
  (`oncall` is `CYCLOPS_SLACK_ONCALL`, `platform-certs` is
  `CYCLOPS_SLACK_PLATFORM_CERTS`). One function produces the name, called
  by the controller when it wires the Pod and by report mode when it reads,
  so the two can't disagree; report mode computes names from the CR it
  loaded and never scans the environment. A missing variable fails with a
  message naming the target.
  - The CRD restricts a target's `name` to lowercase letters, digits and
    `-`. `_` is not allowed, so `on-call` and `on_call` can't map to the
    same variable. The maximum length is set with the CRD.
  - Names are unique across all the Slack targets of one `CertReport`, not
    only within one notifier, because they share one Pod. Different
    `CertReport`s have separate CronJobs, so they may reuse a name.
  - Why not the target's position: the CronJob is updated a moment after
    the CR. A report that runs in that gap, after the targets were
    reordered, would read the wrong variable and post to the wrong channel
    with no error. With names, reordering is harmless and a new or renamed
    target fails loudly. Position-based names are also unreadable in a
    large install.
- **When to post** (question 2, option A). As email (ADR 0015): only when
  there are findings, plus a one-time welcome when the `CertReport` is
  created. As ADR 0015 amended it, the welcome carries no findings. For
  Slack it also checks that the URL works.
- **Authentication** (question 3). None beyond the URL, which is itself the
  credential. Headers and HMAC stay open for a generic webhook.
- **Failures and retries** (question 4, option A). Retried inside the
  process, not by the Job: 10s per attempt; network errors, 429 (honouring
  `Retry-After`) and 5xx are retried up to 3 times with exponential
  backoff; other 4xx are never retried. Each target succeeds or fails on
  its own, so one failing does not stop the others. The Job exits non-zero
  if any failed, and its `backoffLimit` stays 0, so nothing already
  delivered is sent again. ADR 0014/0015's email Jobs should match; that is
  checked separately, not changed by this ADR.
- **Reports larger than a message.** Slack limits a message (50 blocks, and
  3000 characters in a text section, per Slack's documentation; not yet
  tested against Slack). A report that does not fit is split into several
  messages, in order, spaced to stay under Slack's rate limit of about one
  message per second per webhook. If a later message fails after its
  retries, earlier ones stay posted and the error names the part that
  failed. Text is escaped for Slack (`&`, `<`, `>`).
- **The URL never appears in an error or a log line.** The sender
  (`webhooks.Sender`, one per target) holds the URL, and scrubs it from
  every error it returns, including its constructor's and the HTTP
  client's. Go's HTTP errors include the full URL, which was checked. Errors
  name the target by its `name`. This follows Flux's notification-controller,
  which masks secret addresses where errors surface and redacts them as
  `scheme://host/***`.

- **The CRD shape** (decided 2026-11-02, the sketch as it stood):
  ```yaml
  notifiers:
    - type: Slack
      slack:
        targets:
          - name: oncall
            urlSecretRef: {name: slack-oncall}   # key fixed as `url`
  ```
  - `targets` is a map list keyed by `name` (1 to 20 items). A `name` is 1
    to 63 characters of lowercase letters, digits and `-`. A Secret name is
    a DNS subdomain.
  - A CEL rule on the spec makes names unique across all the Slack
    notifiers of a `CertReport`. To keep that rule within the API server's
    cost budget, `notifiers` now has `maxItems: 10` (ADR 0018 set only
    `minItems: 1`).
  - Why a `urlSecretRef` object rather than a plain `secretName` string:
    it matches `emailTemplateConfigMapRef`, and a field can be added to it
    later (a different key, say) without breaking the API.
- **Checking the Secrets.** Each target's Secret is checked into the
  existing `CredentialsFound` condition (as question 1, option A, said),
  reasons `SecretNotFound` and `SecretKeyMissing`, naming the target. The
  mail credentials Secret is now checked only for a `CertReport` with an
  Email notifier, so a webhook-only report does not wait on mail settings.
  Likewise the Helm chart's email settings are optional (2026-11-02): with
  no SMTP host or SES region it installs no mail ConfigMap, and a
  `CertReport` with an Email notifier fails its runs saying email is not
  configured.
- **The Pod's env var references are optional.** With a required one, a
  missing Secret holds the Pod in `CreateContainerConfigError`, and with
  `concurrencyPolicy: Forbid` that stuck run blocks every later one.
  Optional, the run starts and fails with a message naming the target.
- **A changed Secret sends a new welcome** (decided 2026-11-02; **not
  built yet**, planned for the beta after `beta.8`). The aim: a rotated
  URL is checked at once rather than at the next report with findings.
  - Built so far: each Slack target's Secret `resourceVersion` is part of
    the welcome hash, as the mail Secret's is. As ADR 0015 works, that only
    retries a *failed* welcome once the Secret is fixed. After a welcome
    has succeeded nothing sends another, so a rotated URL gets none (seen
    on kind, 2026-11-02).
  - Planned: record each target's Secret version when its welcome is sent,
    and send a welcome to just the targets whose version has changed (or
    that were added later). This amends ADR 0015's once-only rule for
    Slack targets.
  - A `CertReport` with only Email notifiers hashes exactly as before, so
    upgrading re-sends no welcome.

## Still open

- **Who may use which Secret.** Nothing restricts which Secrets a
  `CertReport` may reference. Isolating teams from each other's channels
  would be its own decision.
- **A generic `type: Webhook`**, its JSON schema (question 6), its
  authentication, and Teams. Also whether ADR 0007's "v1 ships both Email
  and Webhook" now means Slack only.
- **Email's Job retry settings** following question 4 (see above).

## Open questions
(Kept for the options considered. Each says what Slack settled.)

### 1. Where is a webhook notifier configured?
*Settled for Slack: option A, one Secret per webhook (see above).*

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
*Settled for Slack: option A (see above).*

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
*Settled for Slack: the URL alone. Still open for a generic webhook.*


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
*Settled: option A (see above).*

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

### 5. Chat services need their own payload formats
*Settled for Slack: option B, as its own `type: Slack`. Teams is still open.*

This may reopen ADR 0007's payload decision.

The receivers people will most likely point cyclops at don't accept
arbitrary JSON; the body has to match what the service expects:
- **Slack incoming webhooks** need `text` and/or `blocks` (Block Kit).
  Slack Workflow Builder webhooks accept custom keys, but only flat,
  declared variables.
- **Teams** has moved from Office 365 connectors (MessageCard) to Workflows
  (Power Automate) webhooks, which typically expect an Adaptive Card. A
  flow can parse arbitrary JSON, but someone has to build it.
- The Teams details need verifying before deciding; the platform has
  changed more than once.

Options:
- **A. Generic schema only, as ADR 0007 says** (the Alertmanager model)
  - Pros: one stable contract; no need to track vendor formats.
  - Cons: Slack/Teams users need a relay or adapter (or a Slack workflow /
    Power Automate flow) to reshape the payload, which is extra
    infrastructure for the most common case.
- **B. Built-in formats: `format: Generic | Slack | Teams`**
  - Cyclops emits a native Block Kit or Adaptive Card payload itself.
  - Pros: works directly with an incoming webhook URL.
  - Cons: more code and tests; the Teams format in particular has changed
    before and could change again; every new chat service asks for a new
    format.
- **C. User-templated payloads** (`text/template` over the ADR 0013 data)
  - Pros: fits any receiver.
  - Cons: ADR 0007 rejected this deliberately: a second templating engine,
    JSON escaping pitfalls, and no stable contract for programs.
- **D. Generic schema plus a top-level `text` summary**
  - Slack incoming webhooks render `text` and ignore unknown fields, so
    Slack would show a one-line summary with no relay. Teams still needs a
    flow.
  - Pros: a small addition to A.
  - Cons: only a partial answer, and it relies on Slack ignoring the
    extra keys.

If B or D is chosen, question 3 (auth) changes: Slack and Teams
authenticate through the secret URL alone, not a header or signature.

### 6. The exact JSON schema
*Open. Slack does not need it; it applies to a generic `type: Webhook`.*

ADR 0007 fixes the approach: a versioned schema, the same for every
receiver, with no templating, following Alertmanager's webhook receiver.
Still to settle:
- the field names, likely mirroring the `TemplateData`/`Row` contract from
  ADR 0013;
- the version field;
- how the welcome payload is marked, if question 2 settles on A.

This depends on question 5: with option B, "the schema" is one generic
schema plus each built-in format.
