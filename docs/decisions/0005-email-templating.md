# 5. Email templating

## Status
Decided. The template fields in the example below are illustrative only; the
actual data contract is [ADR 0013](0013-template-data.md).

## Context
Reports need a default rendering, with the ability for a user to supply
their own HTML template. Two sub-questions: where does a custom template
live, and what templating engine renders it.

## Template source

**Inline string field in the CRD spec**
- Pros: everything in one object, GitOps-friendly single manifest.
- Cons: CRDs have a practical size ceiling (etcd object size limits); a
  rich HTML template inline makes the CR unwieldy to read/diff; couples
  "config" with "presentation."

**ConfigMap reference** (`spec.emailTemplateConfigMapRef: {name, key}`)
- Pros: keeps the CR small and focused on config; template is its own
  reviewable artifact; no CRD size concerns even for large templates;
  cleanly separates "who owns alerting policy" from "who owns the visual
  template."
- Cons: one more object to manage/RBAC for; controller needs to watch the
  ConfigMap too, to pick up template edits.

**Both, inline taking precedence if set**
- Pros: maximum flexibility.
- Cons: two code paths, ambiguity rules to document for precedence.

## Decision: ConfigMap reference only
`spec.emailTemplateConfigMapRef` pointing at a ConfigMap key. Omitting it
uses a built-in default template.

## Templating engine

Go's standard library `html/template`, not a hand-rolled string-replace
approach or a third-party templating library. This matters concretely
because the `Reason` text passed through from ADR 0004 originates from an
external ACME server/CA and is untrusted input — `html/template`
auto-escapes by context (HTML/JS/URL), so a stray `<` or `&` in that text
renders as literal text rather than being interpreted as markup. A
third-party templating library would add a dependency for no benefit over
what the standard library already provides here.

## Example shape (illustrative)

```yaml
apiVersion: cyclops.io/v1alpha1
kind: CertReport
metadata:
  name: cluster-cert-report
spec:
  emailTemplateConfigMapRef:
    name: cert-report-template
    key: report.html.tmpl
```

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cert-report-template
data:
  report.html.tmpl: |
    <html><body>
      <h2>Certificates expiring within {{ .ThresholdDays }} days</h2>
      <table>
        {{ range .Certificates }}
        <tr><td>{{ .Namespace }}</td><td>{{ .Name }}</td>
            <td>{{ .NotAfter }}</td><td>{{ .State }}</td>
            <td>{{ .FailedIssuanceAttempts }}</td><td>{{ .Reason }}</td></tr>
        {{ end }}
      </table>
    </body></html>
```
