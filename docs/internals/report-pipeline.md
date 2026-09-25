# Report pipeline

What report mode does with cert-manager's data: turn it into cyclops's own view of each
certificate, decide which ones need attention, put them in a stable order, and render the email.

```
list ──────────────▶ convert ──────────────▶ evaluate ─────────▶ render ──────────────────▶ send
internal/certmanager  internal/certmanager     internal/report     internal/render              (not built yet)
List → Snapshot       Snapshot.CertStatuses    Evaluate            NewTemplateData → Render,
                      (ToCertStatus per cert)                      Subject
```

| Package | Knows about | Tested with |
|---|---|---|
| `internal/certmanager` | cert-manager's API types; the only package that imports them | hand-built objects and controller-runtime's fake client, no cluster |
| `internal/report` | plain Go: `CertStatus`, `Finding`, `time.Time` | table tests, fixed `now` |
| `internal/render` | `report.Finding` and `html/template`; the template data contract | table tests, rendered-output checks |
| `test/cluster` | the whole pipeline up to `Evaluate` | a real kind cluster with cert-manager (`make test-cluster`) |

Keeping cert-manager at the edge means the decision logic can be tested without a cluster, and a
cert-manager API change only touches one package.

---

## `List`: reading from the cluster

`internal/certmanager/list.go`: `List(ctx, c, namespaces) (Snapshot, error)`

`Snapshot` holds everything read in one run: the Certificates, CertificateRequests, Orders and
Challenges as four separate lists. The lists are linked to each other
only by owner references; `Snapshot.CertStatuses()` joins them into one `CertStatus` per
Certificate (see [`ToCertStatus`](#tocertstatus-converting-cert-manager-objects)).

Which namespaces are read follows ADR 0010:

- **Empty `namespaces` → all namespaces.** It becomes the one-item list `[""]`, because
  `client.InNamespace("")` means "all namespaces". Each kind is then fetched with one
  cluster-wide `List`.
- **Explicit namespaces → one `List` per kind per namespace.** Duplicates are removed first, so a
  namespace listed twice doesn't put its certificates in the report twice.
- **A namespace that doesn't exist contributes nothing.** Listing inside it returns an empty list
  with no error. Catching typos and deleted namespaces is the controller's job: it sets a
  `NamespacesFound` condition on the `CertReport` (ADR 0011).

Certificates aren't sorted here; `Evaluate` sorts the findings.

`List` fetches everything in one go, with no pagination. That's fine for normal clusters (tens to
thousands of certificates). At very large scale, listing is the expensive step, and
`client.Limit`/`Continue` pagination is where to start.

---

## `CertStatus`: cyclops's view of a certificate

`internal/report/status.go`

| Field | Type | Zero value means | Source |
|---|---|---|---|
| `Namespace`, `Name` | `string` | — | `Certificate.metadata` |
| `NotAfter` | `time.Time` | **never issued** | `Certificate.status.notAfter` |
| `RenewalTime` | `time.Time` | no renewal scheduled | `Certificate.status.renewalTime` |
| `FailedIssuanceAttempts` | `int` | no failures since last success | `Certificate.status.failedIssuanceAttempts` |
| `State` | `string` | nothing to report | failing Challenge or Order (below) |
| `Reason` | `string` | nothing to report | same object as `State`, copied verbatim |

`NotAfter` keeps the X.509 name for "expiry date". Read it as *ExpiresAt*: the "Not" is not a
negation.

The last four are failure diagnostics (ADR 0004). They are relayed to the reader as-is. `Reason`'s
wording changes across cert-manager versions and ACME servers, so it's for humans to read, never
for code to match on.

---

## `Evaluate`: which certificates go in the report

`internal/report/evaluate.go`: `Evaluate(certs, now) []Finding`

Three questions per certificate, in order; the first "yes" decides (ADR 0012):

| # | Check | Code | Kind |
|---|---|---|---|
| 1 | Is there an expiry date at all? | `c.NotAfter.IsZero()` | `NeverIssued` |
| 2 | Is now after the expiry date? | `now.After(c.NotAfter)` | `Expired` |
| 3 | Is the renewal more than an hour late? | `isOverdue(c, now)` | `RenewalOverdue` |

If all three are "no", the certificate is fine and isn't reported.

**There's no threshold.** cert-manager sets `status.renewalTime` on every issued certificate (by
default 2/3 of the way through its lifetime) and starts renewing then. A successful renewal moves
it forward, so a `renewalTime` in the past means renewal is due and hasn't happened. Each
certificate gets its own window, with nothing to configure.

**Inclusion is by dates only (ADR 0009).** A certificate with 5 failed attempts whose renewal isn't
due yet is not reported. The diagnostics are shown for certificates that are already in the
report, so the reader can see why a due certificate hasn't renewed.

### Reading the time comparisons

`a.After(b)` means **a > b**: whatever comes before the dot is the left side. Go can't use `<`/`>`
on `time.Time` (it's a struct), so the standard library provides `Before`, `After`, `Equal` and
`Compare`.

`isOverdue` is:

```go
if c.RenewalTime.IsZero() {
	return false                                  // no renewal scheduled
}
if now.After(c.RenewalTime.Add(RenewalGrace)) {  // now > renewalTime + 1h
	return true
}
return false
```

Example with `now` = Sep 24, 12:00:

| Cert | `NotAfter` | `RenewalTime` | Result |
|---|---|---|---|
| expired-cert | Sep 20 | Aug 20 | `Expired` (check 2 wins, even though it's also overdue) |
| stuck-cert | Oct 05 | Sep 14 | `RenewalOverdue`: 10 days late |
| renewing-cert | Nov 01 | Sep 24, 11:30 | not reported: only 30 minutes late, inside the grace |
| fine-cert | Dec 25 | Nov 25 | not reported: renewal not due |

### Boundaries

- **`RenewalGrace` is 1 hour, strictly after.** cert-manager starts renewing at `renewalTime`, so a
  certificate is briefly past it while a normal renewal runs. Exactly 1 hour late is not reported;
  1 hour and 1 nanosecond is.
- **No `renewalTime` → never overdue.** The zero time plus an hour would be "overdue since year 1",
  so `isOverdue` checks `IsZero` first.
- **Exactly at `NotAfter` is not expired yet.** Matches `crypto/x509`: a certificate is valid at
  the instant of `NotAfter` and expired only after it.

All three have test cases in `evaluate_test.go`.

### Sort order

Findings are sorted by `NotAfter`, then namespace, then name.

- **Most urgent first.** The zero time sorts before every real date and past dates before future
  ones, so sorting by `NotAfter` alone gives `NeverIssued` → `Expired` → soonest-expiring
  `RenewalOverdue`.
- **Stable from day to day.** The API server returns objects in no guaranteed order. The
  namespace/name tie-break means the same certificates always appear in the same order, so today's
  email can be compared with yesterday's.

The sort runs on findings, not all certificates, so it's small. It stays synchronous because
rendering needs its result; there's nothing to overlap it with.

---

## `ToCertStatus`: converting cert-manager objects

`internal/certmanager/convert.go`

```go
func ToCertStatus(
    cert *cmapi.Certificate,
    reqs []cmapi.CertificateRequest,
    orders []acmev1.Order,
    chals []acmev1.Challenge,
) report.CertStatus
```

A pure function: it makes no API calls. `Snapshot.CertStatuses()` calls it once per Certificate,
passing every CertificateRequest, Order and Challenge `List` fetched, for all certificates;
`ToCertStatus` picks out the ones belonging to `cert`.

### The plain fields

`NotAfter`, `RenewalTime` and `FailedIssuanceAttempts` are **pointers** in cert-manager
(`*metav1.Time`, `*int`), `nil` until known. A never-issued certificate has `NotAfter == nil`.
Dereferencing `nil` panics, so each is checked first and left at its zero value when `nil`.
`timeOrZero` does this for the two timestamps.

### The ownership chain

The useful error text isn't on the Certificate. It's further down a chain of objects cert-manager
creates:

```
Certificate "web"                       ← you create this once
 └── CertificateRequest "web-3"         ← one per issuance attempt (renewal, retry)
      └── Order "web-3-2873460"         ← ACME issuers only; one per request
           ├── Challenge "...-1"        ← one per domain: "prove you own example.com"
           └── Challenge "...-2"
```

The names look related, but **ownership is by UID, not by name.** Every object has a unique
`metadata.uid`, and cert-manager writes the parent's UID into each child's
`metadata.ownerReferences` with `controller: true`:

```yaml
# CertificateRequest
metadata:
  name: web-3
  ownerReferences:
  - kind: Certificate
    uid: aaaa-1111      # ← the Certificate's metadata.uid
    controller: true
```

`metav1.IsControlledBy(child, parent)` checks exactly that: the child's controller owner reference
has the parent's UID. It's the only check needed at each level.

### Choosing `State` and `Reason`

1. Take the **newest** CertificateRequest owned by the Certificate (by creation time). Older ones
   describe past attempts. None → leave `State`/`Reason` empty.
2. Take the **Order** owned by that request. None → leave them empty: this is normal for non-ACME
   issuers (private CA, Vault, self-signed), which never create Orders.
3. Among that Order's **Challenges**, ignore the healthy ones. If any are left, use the first by
   name. Challenges win over the Order because their `Reason` is the most specific.
4. Otherwise, if the Order isn't healthy, use the Order's.
5. Otherwise, leave both empty.

"Healthy" is `healthyStates`, which contains only `valid`. Everything else is reported, including
transient states like `pending` and any state cert-manager adds in the future.

| Challenges | Order | Result |
|---|---|---|
| a: `valid`; b: `pending`, "Waiting for HTTP-01 challenge propagation: connection refused" | `pending`, no reason | b's: `pending`, "…connection refused" |
| a: `invalid` "a failed"; b: `invalid` "b failed" | `invalid` | a's: `invalid`, "a failed" |
| all `valid` | `errored`, "Failed to finalize Order: 429 rate limited" | the Order's |
| all `valid` | `valid` | empty |

Why the rules are the way they are:

- **Healthy Challenges are skipped** so a `valid` Challenge with an empty reason can't hide a
  failing one (first row).
- **"First by name"** keeps the email stable when several fail, since the API server returns them
  in no fixed order (second row).
- **`pending` counts.** The most common stuck case is a Challenge sitting in `pending` whose Reason
  already says why the self-check keeps failing. Reporting only "failed" states would drop it.
- **Empty means "nothing to say".** cyclops never writes its own diagnostic text (ADR 0004). An empty
  State matches cert-manager's own `acmev1.Unknown`, which is `""`.

### ACME states

`acmev1.State` is a named `string` type shared by Orders and Challenges. The values come from the
ACME spec (RFC 8555) and are set by cert-manager from the ACME server's responses; cyclops only
reads them.

| Constant | Value | Kind |
|---|---|---|
| `acmev1.Unknown` | `""` | not set |
| `acmev1.Pending` | `pending` | transient |
| `acmev1.Processing` | `processing` | transient |
| `acmev1.Ready` | `ready` | transient |
| `acmev1.Valid` | `valid` | final, success |
| `acmev1.Invalid` | `invalid` | final, failure |
| `acmev1.Expired` | `expired` | final, failure |
| `acmev1.Errored` | `errored` | final, failure |

```
pending ──▶ processing ──▶ ready ──▶ valid
   │            │
   └────────────┴──▶ invalid / expired / errored
```

To look these up yourself: `go doc github.com/cert-manager/cert-manager/pkg/apis/acme/v1.State`.

---

## Known gap: no reason for non-ACME failures

`State` and `Reason` only come from ACME Orders and Challenges (ADR 0004). A certificate from any
other issuer (CA, Vault, self-signed) that fails to issue reaches the report with both empty.

Found by the cluster tests: `cyclops-test-b/broken` is reported as `NeverIssued` with no reason.
cert-manager does record why, just not on the Certificate:

```
Certificate broken   Ready=False  DoesNotExist   "Issuing certificate as Secret does not exist"
CertificateRequest   Ready=False  Pending        "Referenced issuer does not have a Ready status condition"
Issuer broken-ca     Ready=False  ErrGetKeyPair  "Error getting keypair for CA issuer: secrets "does-not-exist" not found"
```

The Certificate's `DoesNotExist` is about its own TLS Secret, which every certificate lacks before
its first issuance, so it says nothing useful. The root cause is on the Issuer. Which of these to
relay is an open decision (see [`../decisions/README.md`](../decisions/README.md)).

---

## `render`: turning findings into the email

`internal/render`

```go
data := render.NewTemplateData(reportName, now, findings) // findings from Evaluate
body, err := render.Render(render.Default, data)          // HTML body
subject := render.Subject(data)                           // "[cyclops] 2 certificates need attention"
```

### The template data (ADR 0013)

Every template, the built-in one and custom ones, receives a `TemplateData`: `ReportName`,
`GeneratedAt`, and `Findings` as a flat list of `Row`s (`Kind`, `Namespace`, `Name`, `NotAfter`,
`DaysLeft`, `RenewalTime`, `FailedIssuanceAttempts`, `State`, `Reason`). It is a separate type
from `report.Finding` on purpose: users' templates depend on these field names, so internal types
can change behind it without breaking anyone. Fields may be added; renaming or removing one needs
a new ADR.

`NewTemplateData` builds it:
- **Times are converted to UTC**, so the email doesn't depend on the Job's timezone.
- **`DaysLeft` rounds down** with `math.Floor`: 0.9 days → `0`, half a day past expiry → `-1`.
  A plain `int()` conversion would round towards zero and give `0` for the second. It's `0` when
  the certificate was never issued, rather than the distance back to year 1.

### Rendering

- **`Default`** is the built-in template, `default.html.tmpl`, compiled into the binary with
  `//go:embed` and parsed once at startup. Users change the email with their own template via a
  ConfigMap (ADR 0005), not by editing this file.
- **`Parse`** parses template text, e.g. a custom template.
- **`Render`** executes a template into a `bytes.Buffer` and returns the text only if the whole
  template succeeded. A template that fails halfway (say, `{{ .NoSuchField }}`) returns an error
  and no body, so a broken template never sends half an email. A nil or never-parsed template also
  returns an error, instead of panicking.
- **`Subject`** is set in code, not by the template: "All certificates healthy", "1 certificate
  needs attention", or "n certificates need attention".

### The default template

- **Escaping.** `html/template` escapes every value by context, so `Reason` text (which comes from
  ACME servers and CAs, i.e. untrusted) can't inject markup: `<b>` becomes `&lt;b&gt;`.
- **Inline styles.** Email clients strip `<style>` blocks and don't support CSS variables, so
  every element carries its own `style="..."`.
- **`<meta charset="utf-8">`.** Without it, clients guess Windows-1252 and `—` shows as `â€”`.
  When sending is built, the email also needs a `Content-Type: text/html; charset=UTF-8` header.
- **Empty report.** No findings renders an "all clear" body. Whether to send it at all is part of
  the open dedup decision.

---

## Not built yet

- **Sending** via the notifiers: SES and SMTP (ADR 0007/0008).
- **Custom templates** from a ConfigMap (ADR 0005), which needs the `CertReport` schema.
- **Report mode** in `cmd/main.go`, which wires `List` → `Evaluate` → render → send (ADR 0006).
- **Dedup** across days is an open decision and may change what `Evaluate` returns.
