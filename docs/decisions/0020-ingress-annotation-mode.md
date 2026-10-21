# 20. Ingress annotation mode

## Status
Accepted.

## Context
ADR 0003 fixes what we watch (cert-manager `Certificate` resources) and ADR 0010
fixes where (`spec.namespaces`, empty = all). Both work per namespace. That is
too coarse when one namespace holds resources with different owners or
lifecycles: a namespace `tenant` may hold `ingress-qa` and `ingress-prod`, and
a team may want to watch one and not the other.

Annotation mode lets an Ingress opt in to a specific `CertReport` with a single
annotation. The CR then uses its own schedule and recipients for every Ingress
that names it. This gives users fine-grained control over what is watched and,
more importantly, what is left out, and it is easier to manage than listing
resources by hand in the CR. The mode is opt-in per CR, so existing CRs behave
as before.

The watch target stays the `Certificate` (ADR 0003). The annotation selects
Ingresses only to find the Certificates behind them.

## Options considered

**A. Annotation on the Ingress, mode flag on the CR** (chosen)
- Pros: opt-in is done by whoever owns the Ingress, with no CR edit; can
  separate qa from prod inside one namespace; works alongside CRs that use
  `spec.namespaces`.
- Cons: needs read access to Ingresses (revises ADR 0010); any user who can
  edit an Ingress can point it at any CR.

**B. Label selector on the CR**
- Pros: the CR owner stays in control of what is included.
- Cons: Ingress owners cannot opt in themselves, so it is manual setup again;
  needs a selector design and the same Ingress read access.

**C. Namespaces only (status quo)**
- Pros: nothing new to build or secure.
- Cons: cannot separate resources that share a namespace.

## Decision
Option A.

- **Mode:** a plain bool on the CR, `spec.ingressAnnotationMode: true`. In
  annotation mode only annotated Ingresses are in scope. Scope is never the
  union of the two: a CEL rule rejects a CR that sets both the mode and
  `spec.namespaces`, so a list the author thought was active is never
  silently unused. The `NamespacesFound` condition does not apply in this
  mode: it stays `True` with reason `AnnotationMode`, so the status shape is
  stable.
- **Annotation:** one key, `cyclops.sakshitposting-irl.github.io/cr-name:
  <CertReport name>`. Its presence is the opt-in; there is no separate watch
  key.
- **Mapping:** each `spec.tls[].secretName` on the Ingress is matched to
  `Certificate.spec.secretName` in the same namespace. The matching
  Certificates go through the normal inclusion rule (ADR 0012).
- **Report content:** each row names the Ingress it came from, as an
  additive change to the template data (ADR 0013). Two Ingresses can share a
  `secretName`, so the field holds a list (e.g. `Ingresses []string`). It is
  empty in namespace mode, and the default template hides it when empty.
- **Guard:** none in v1.
- **Dangling annotation:** an annotation naming a CR that does not exist, or
  an Ingress whose secret has no Certificate, is skipped
  silently: no log line, no Event and no condition. Checking that an annotation
  points at something real is left to the Ingress owner, as with other
  annotation-driven controllers.
- **RBAC:** `get/list/watch` on `networking.k8s.io` `ingresses`, added to the
  ClusterRole and gated in the Helm chart.

## Consequences
- ADR 0010's read-only ClusterRole gains Ingresses.
- Cross-tenant risk: anyone who can edit an Ingress can send that Ingress's
  certificate details (hostnames, names, dates, failure reasons) to a CR's
  recipients. This cannot expose Secrets, and the CR cannot claim an Ingress
  that does not name it. Mitigate with RBAC on who can edit Ingresses. An
  optional allowlist field (e.g. `allowedNamespaces`) can be added later
  without breaking existing CRs.
- A typo in the annotation fails silently from the user's side: the Ingress is
  just never reported, and nothing is logged. This
  follows the usual behaviour of annotations a controller does not act on,
  but it is the main thing to document in the README.
- Gateway API (`HTTPRoute`/`Gateway`) is out of scope for v1.
- The report Pod needs `get/list` on Ingresses too, so its service account's
  permissions must include them (`config/rbac` and the Helm chart).
- Does not change dedup (ADR 0015) or observability.
