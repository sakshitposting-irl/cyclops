# 2. CRD scope

## Status
Decided

## Context
A CRD's scope (Cluster vs Namespaced) is fixed at the CustomResourceDefinition
level — it cannot be a per-instance switch. Two idiomatic ways exist to still
get scope flexibility: a single cluster-scoped Kind with a spec field
controlling watch breadth, or two Kinds mirroring cert-manager's own
`Issuer`/`ClusterIssuer` pattern.

## Options considered

**A. Single cluster-scoped CRD (`CertReport`) with a namespace-filtering
field in spec**
- Pros: one Kind, one controller reconcile path, one RBAC surface; still
  gives per-config flexibility on *what it watches* via a
  `namespaces`/`namespaceSelector` field.
- Cons: doesn't allow two teams to each own an independent report config in
  their own namespace — it's still one shared object (or a few, cluster-
  scoped, distinguished by name).

**B. Two Kinds — `CertReport` (namespaced) + `ClusterCertReport`
(cluster-scoped)**
- Pros: true dual-scope, matches the exact pattern cert-manager itself
  uses (`Issuer`/`ClusterIssuer`).
- Cons: two Kinds to maintain, two RBAC entries, and precedence/merge rules
  needed if both a namespaced and cluster-scoped one exist and could
  overlap on the same secret/cert — real added complexity for a
  requirement not present in the stated goal.

## Decision
**Option A**: a single **cluster-scoped `CertReport` Kind**, with a spec
field (`namespaces`/`namespaceSelector`) controlling watch breadth. The
stated goal describes a single cluster-wide daily digest to one configured
recipient list, not per-team ownership, so the added complexity of two
Kinds isn't justified. Should independent per-namespace ownership become a
real requirement later, that's a case for revisiting this decision, not for
retrofitting silently.
