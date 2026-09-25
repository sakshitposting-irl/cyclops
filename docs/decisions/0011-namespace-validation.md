# 11. Validating `spec.namespaces` in the controller

## Status
Decided. Supersedes the "missing namespaces are reported" part of ADR 0010.

## Context
ADR 0010 made report mode check that every namespace in `spec.namespaces`
exists and list any that don't in the email. On reflection, a wrong
namespace is a problem with the `CertReport`'s configuration, not with any
certificate, so the email is the wrong place for it: it repeats every day
and mixes config errors into the certificate report.

A namespace can be wrong in two ways:
- a typo, present from the moment the `CertReport` is created or edited;
- a namespace that existed when the `CertReport` was written and was
  deleted later. The report then silently covers less.

## Options considered

**A. Keep it in the email (ADR 0010 as written)**
- Pros: the most visible place; already implemented.
- Cons: a config problem repeated in every daily email; not where
  Kubernetes users look for "this object is misconfigured".

**B. Validating admission webhook: reject the `CertReport`**
- Pros: a typo fails at `kubectl apply`, immediately.
- Cons: only runs on create/update, so it misses namespaces deleted later;
  needs a webhook server, TLS certificates and a `ValidatingWebhookConfiguration`,
  none of which cyclops otherwise uses.

**C. Controller check: a status condition, kept current by watching Namespaces**
- Pros: catches both typos and later deletions; uses the standard
  Kubernetes place for configuration problems (`status.conditions`); no
  webhook.
- Cons: visible only to someone who looks at the `CertReport`'s status or
  its Events, not pushed to anyone.

## Decision
**Option C.**

- The `CertReport` reconciler checks that each namespace in
  `spec.namespaces` exists and sets a condition:
  - `type: NamespacesFound`, `status: "True"` when all exist (or the list is
    empty, meaning all namespaces);
  - `status: "False"`, `reason: NamespaceNotFound`, with the missing names,
    sorted, in the `message`.
- It emits a `Warning` Event with the same reason when a namespace goes
  missing.
- The controller **watches Namespaces**. When one is created or deleted, it
  re-reconciles the `CertReport`s whose `spec.namespaces` name it, so the
  condition stays current without waiting for the `CertReport` to change.
- Report mode no longer checks namespaces. Listing inside a namespace that
  doesn't exist returns an empty list, so it's simply skipped; nothing about
  missing namespaces goes in the email.

## RBAC changes (relative to ADR 0010)
- Controller: `get`, `list`, `watch` on `namespaces` (for the check and the
  watch), plus `create`/`patch` on `events` and `update` on
  `certreports/status`.
- Report Job: drops `get` on `namespaces`; it no longer needs it.
