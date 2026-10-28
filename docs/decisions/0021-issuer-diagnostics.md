# 21. Diagnostics for non-ACME failures: the request's and the Issuer's Ready condition

## Status
Accepted. Settles the open item "Diagnostics for non-ACME failures". Revises
[ADR 0010](0010-watch-scope.md)'s RBAC: the report Pod can now read Issuers and
ClusterIssuers. Extends [ADR 0004](0004-failure-diagnostics.md) and
[ADR 0013](0013-template-data.md) without changing either's rules.

## Context
ADR 0004 takes `State` and `Reason` from the ACME `Order`/`Challenge` chain. A certificate from a
CA, Vault or self-signed issuer has no Order, so a failure there was reported with no reason at
all: `Failed attempts 0`, `cert-manager says —`. The cluster tests found it with `broken`, whose
Issuer points at a CA Secret that does not exist.

What cert-manager actually records in that case:

- The `Certificate`'s own `Ready` condition does not help: `DoesNotExist` refers to its TLS
  Secret, which is normal before first issuance.
- cert-manager never fails the `CertificateRequest`. It waits, so `failedIssuanceAttempts`
  stays 0. The request's `Ready` condition is `False (Pending)`: "Referenced issuer does not have a
  Ready status condition".
- The root cause is only on the Issuer: `Ready=False (ErrGetKeyPair)`: `secrets "does-not-exist"
  not found`.

## Options considered

**A. The request's `Ready` condition only**
- Pros: no new RBAC; same objects already read.
- Cons: for this failure it only says the issuer is not ready, not why.

**B. The request's and the Issuer's `Ready` conditions** (chosen)
- Pros: the email names the root cause. The request's condition still covers failures where the
  issuer is healthy (e.g. a Vault or CA signing error on the request).
- Cons: read access to Issuers and ClusterIssuers (revises ADR 0010). Only status conditions are
  used, never `spec`, so no credentials or Secret names beyond what the condition message
  already contains.

**C. Categorize the messages**
- Rejected for the reason in ADR 0004: wording changes across cert-manager versions.

## Decision
**Option B**, with the text relayed verbatim and nothing interpreted (ADR 0004).

1. **Request condition.** With no Order, `State` and `Reason` are the newest CertificateRequest's
   `Ready` condition `Reason` and `Message` when its status is not `True`. With an Order the ACME
   diagnostics win, as before. A request with no `Ready` condition yet adds nothing.
2. **Issuer condition.** A new `IssuerError` string, `"<Kind> <name>: <Reason>: <Message>"`, from
   the `Ready` condition of the Issuer or ClusterIssuer in `spec.issuerRef` when its status is not
   `True`. It is independent of the Order check, so an ACME ClusterIssuer that cannot register is
   reported too. Empty when the issuer is ready, was not found, has no `Ready` condition yet, or
   is an external issuer (a group other than `cert-manager.io`).
3. **Listing.** `List` reads Issuers per namespace, like the other kinds, and ClusterIssuers once.
   Annotation mode (ADR 0020) inherits it.
4. **RBAC.** The report ClusterRole gets `get`/`list` on `issuers` and `clusterissuers` in
   `cert-manager.io`, in `config/rbac/report_role.yaml` and the chart.
5. **Template data (ADR 0013).** `Row.IssuerError` is added; it is additive, so existing
   templates keep working but do not show it. The default template shows it under the
   "cert-manager says" column, after `State: Reason`. Like `Reason`, it is untrusted text.
6. **Inclusion is unchanged.** Diagnostics never add a certificate to the report (ADR 0009).

## Consequences
- A custom template has to be updated to show `.IssuerError`. The chart README lists it.
- External issuers (AWS PCA, Google CAS...) are not covered; their types are not in this
  package. The request's condition still applies to them.
- The report Pod can read every Issuer's status cluster-wide, in line with how it already
  reads every Certificate.
