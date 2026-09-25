# 10. Watch scope and RBAC

## Status
Decided. The "missing namespaces are reported" part is superseded by
[ADR 0011](0011-namespace-validation.md): namespaces are validated by the
controller instead, and report mode no longer checks them.

## Context
ADR 0002 made `CertReport` cluster-scoped, with a spec field controlling
which namespaces it covers, and left the field's shape open
(`namespaces` vs `namespaceSelector`). This ADR settles that, how report mode
lists objects for each choice, what happens when a listed namespace doesn't
exist, and what RBAC the report needs.

Report mode lists four kinds of object: cert-manager `Certificate` and
`CertificateRequest` (`cert-manager.io`), and ACME `Order` and `Challenge`
(`acme.cert-manager.io`), the last three for failure diagnostics (ADR 0004).

## Options considered

**A. Always the whole cluster, no field**
- Pros: simplest; nothing to misconfigure.
- Cons: no way to limit a report to the namespaces a team cares about;
  ADR 0002 already committed to a scoping field.

**B. `spec.namespaces: [...]`, omitted or empty = all namespaces**
- Pros: easy to read and write; matches the common Kubernetes convention for
  "empty means all"; forgetting the field over-reports rather than
  under-reports, which is the safe direction for a backstop.
- Cons: new namespaces must be added by hand to be covered (unless the list
  is empty); a typo silently matches nothing unless handled explicitly
  (see below).

**C. `spec.namespaceSelector` (label selector)**
- Pros: new namespaces are picked up automatically when labelled.
- Cons: depends on namespaces being labelled consistently, which many
  clusters don't do; a selector matching nothing fails silently just like a
  typo; needs `list`/`watch` on namespaces. More machinery than the stated
  goal needs.

**D. Include and exclude lists**
- Pros: "everything except `kube-system`" is expressible.
- Cons: two fields and precedence rules for a need nobody has stated yet.
  Can be added later without breaking B.

### How "all namespaces" is listed
- **One cluster-wide `List` per kind** (no namespace set on the request).
  One call per kind, and it also covers namespaces created mid-run.
- Rejected: listing namespaces first and then listing inside each. That's
  N+1 calls, needs `list` on namespaces, and can miss namespaces created
  while it runs.

An explicit list is fetched with one `List` per kind per namespace
(`client.InNamespace(ns)`).

## Decision
**Option B.** `spec.namespaces` is a list of namespace names. Omitted or
empty means all namespaces, fetched with one cluster-wide `List` per kind.
A non-empty list is fetched namespace by namespace.

**Missing namespaces are reported, not ignored.** Listing a namespace that
doesn't exist returns an empty result, not an error, so a typo would
otherwise look like "all certificates are fine". Report mode checks that
each listed namespace exists and includes any that don't in the report
("namespace `prodd` not found"). The check runs on every report, so a
namespace deleted after the `CertReport` was written is caught as well.
Surfacing this on the `CertReport` status too is a possible follow-up, not
part of this decision.

**RBAC is a ClusterRole in both cases.** `CertReport` is cluster-scoped and
the namespace list can name any namespace, so the report needs, cluster-wide:
- `get`, `list` on `certificates` and `certificaterequests` (`cert-manager.io`)
- `get`, `list` on `orders` and `challenges` (`acme.cert-manager.io`)
- `get` on `namespaces` (core), for the missing-namespace check

No `watch`: report mode lists once and exits. No write access to any
cert-manager resource.

**`spec.namespaces` limits what goes in the report. It is not a security
boundary.** The report's permissions are cluster-wide read regardless of the
list. Restricting what cyclops *can* read would need namespaced Roles and is
out of scope; if that becomes a requirement, revisit this decision.
