# 8. Email providers: SES and SMTP, both in v1

## Status
Decided

## Context
Within the `Email` notifier type (ADR 0007), the underlying transport can
be AWS SES or generic SMTP. The original stated goal named both
explicitly ("via ses/smtp"), unlike webhook which surfaced later in
design discussion.

## Options considered

**Both, as `provider: SES | SMTP` under the Email notifier**
- Pros: matches the original stated goal directly; covers both cloud-
  native (AWS) and self-hosted/on-prem SMTP relay use cases without
  forcing a choice; SMTP is often the only option available on non-AWS or
  on-prem clusters, so deferring it could block real usage.
- Cons: two credential shapes to support (AWS creds/IAM vs SMTP
  host/user/pass/TLS settings), two send-path implementations, two things
  to test.

**SES only for v1, SMTP in v2**
- Pros: smaller v1 surface, one credential shape to get right first.
- Cons: contradicts the original stated goal, which named both up front
  (unlike webhook, which was newly introduced scope).

## Decision
**Both SES and SMTP** are in scope for v1, as `provider: SES | SMTP` under
the `Email` notifier type from ADR 0007.

## Open follow-on
Credential handling specifics for each provider (IAM role vs access keys
for SES; host/user/password/TLS settings and Secret shape for SMTP) are
not yet decided — see the open items list in the decisions README.
