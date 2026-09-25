# 13. Template data contract

## Status
Decided. Replaces the illustrative template fields in ADR 0005's example
(`.ThresholdDays`, `.Certificates`).

## Context
ADR 0005 decided the engine (`html/template`), a built-in default template,
and a ConfigMap override for custom templates. It left open exactly what
data a template receives. Once people write their own templates against
those field names, renaming or removing one breaks them, so the data is a
public contract, not an internal detail.

## Options considered

**A. Pass internal types (`[]report.Finding`) straight to the template**
- Pros: no extra type or conversion.
- Cons: templates are coupled to internal structure (`{{ .Cert.Name }}`),
  so any internal refactor can break users' templates; values a reader
  wants, like days until expiry, would have to be computed in the template.

**B. A dedicated, flat data type built for templates**
- Pros: a stable surface that internal code can change behind; values are
  precomputed and ready to print; field names read naturally
  (`{{ .Name }}`).
- Cons: one small type and a conversion step to maintain.

## Decision
**Option B.** Every template, default or custom, is executed with:

```go
type TemplateData struct {
	ReportName  string    // the CertReport's name
	GeneratedAt time.Time // when the report ran, UTC
	Findings    []Row     // most urgent first (Evaluate's order); may be empty
}

type Row struct {
	Kind                   string    // "NeverIssued" | "Expired" | "RenewalOverdue"
	Namespace              string
	Name                   string
	NotAfter               time.Time // UTC; zero if never issued
	DaysLeft               int       // whole days until NotAfter, rounded down; negative once expired
	RenewalTime            time.Time // UTC; zero if none scheduled
	FailedIssuanceAttempts int
	State                  string    // verbatim from cert-manager (ADR 0004); may be empty
	Reason                 string    // verbatim from cert-manager (ADR 0004); may be empty
}
```

- **No threshold and no missing namespaces.** There is no threshold
  (ADR 0012), and namespace problems are reported on the `CertReport`, not
  in the email (ADR 0011).
- **`DaysLeft` is rounded down** (`floor`), so 0.9 days left is `0`
  ("expires today") and half a day past expiry is `-1`. It is only
  meaningful when `NotAfter` is set; templates should check `Kind` for
  `NeverIssued` first.
- **Times are `time.Time` in UTC.** Templates format them as they like
  (`{{ .NotAfter.Format "2006-01-02" }}`); UTC keeps the email independent
  of the Job's timezone.
- **An empty `Findings` still renders** (an "all clear" body). Whether an
  email is sent when there's nothing to report is decided with sending and
  the open dedup item, not here.
- **The subject line is set in code**, not by the template, e.g.
  `[cyclops] 2 certificates need attention`.

**Compatibility.** Adding fields is allowed. Renaming, removing, or changing
the meaning of a field breaks custom templates, so it needs a new ADR and a
migration note.
