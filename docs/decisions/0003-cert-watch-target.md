# 3. What we watch: cert-manager `Certificate` resources, no usage filtering

## Status
Decided

## Context
The goal is to alert on cert-manager renewal failures/upcoming expiry. Two
questions were bundled here: (a) do we watch raw `kubernetes.io/tls`
Secrets or cert-manager's own `Certificate` CRD, and (b) do we scope
reporting to only certificates that are actively "in use" (attached to an
Ingress/Gateway) or report on all cert-manager-managed certificates
regardless of attachment.

## Part A — Secrets vs Certificate CRD

**Filter raw Secrets by the `cert-manager.io/certificate-name` annotation**
- Pros: matches "watch TLS secrets" as literally stated; avoids false
  positives on unrelated TLS secrets.
- Cons: annotations aren't server-side selectable (unlike labels) — the
  informer must watch *all* `kubernetes.io/tls` Secrets in scope and filter
  client-side, costing extra cache memory/events on clusters with many
  unrelated TLS secrets. Also we'd have to parse x509 out of the Secret
  ourselves to get expiry.

**Watch cert-manager's `Certificate` CRD directly**
- Pros: `status.notAfter`/`status.renewalTime`/conditions give us
  structured expiry and renewal-health data for free, no x509 parsing;
  inherently scoped to cert-manager-managed certs only.
- Cons: depends on cert-manager's API being installed — a reasonable
  assumption for a project explicitly built around cert-manager.

Decision: **watch `Certificate` resources.**

## Part B — Should we filter to only "in-use" (host-attached) certificates?

Considered filtering out certificates not referenced by any `Ingress` or
Gateway API resource, on the reasoning that an unused certificate's
renewal failing isn't actionable.

Research finding: cert-manager renewal failures caused by HTTP-01
self-check being broken by ingress-level HTTP→HTTPS redirects (or Gateway
API catch-all redirects) are a long-standing, well-documented, recurring
class of issue (cert-manager/cert-manager#419, #3238, and multiple cloud
provider community threads), not a hypothetical edge case. This confirmed
the diagnostic distinction between "redirected" and "unreachable" failure
causes is worth surfacing (see ADR 0004), but did not change this
attachment-filtering decision.

- Pros of filtering: fewer noisy alerts on abandoned/decommissioned certs.
- Cons of filtering: attachment detection is inherently incomplete — certs
  can be consumed via Ingress, Gateway API, direct Pod volume mounts, or
  service mesh sidecars (Istio/Linkerd), and reliably detecting all of
  these (especially Pod-mount consumption) is nontrivial and not
  exhaustive. A false "unused" classification would suppress a genuine
  alert for a certificate that *is* actually serving production traffic —
  the worst failure mode for a tool whose entire purpose is being a safety
  net against silent renewal failure.

## Decision
Watch **cert-manager `Certificate` resources**, report on **all** of them
in the watched scope (per ADR 0002's namespace filter), with **no
attachment/usage filtering**. A false positive (reporting on an unused
cert) costs a few seconds of reading; a false negative (missing a cert
that's actually serving traffic through an undetected consumption path)
defeats the purpose of the tool.
