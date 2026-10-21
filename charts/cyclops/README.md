# cyclops Helm chart

Installs the cyclops controller, its CRD (`CertReport`), RBAC, and the install-level mail settings
(ADR 0014). **Alpha (v1alpha1): the chart is not published yet; install it from a checkout.**

```bash
helm install cyclops ./charts/cyclops -n cyclops-system --create-namespace \
  --set email.smtp.host=smtp.example.com
```

cert-manager must already be installed. Mail credentials are never set through values: create the
Secret yourself in the release namespace and reference it by name (`email.smtp.credentialsSecretName`
with keys `username`/`password`, or `email.ses.credentialsSecretName` with keys
`access-key-id`/`secret-access-key`; for SES, leave it empty to use IRSA or Pod Identity).

## Customising the email

The chart ships the built-in template as [`files/default.html.tmpl`](files/default.html.tmpl) and
renders it into a ConfigMap (`<release>-email-template`, key `template.html`). It is used only when a
`CertReport` points at it; without the reference, the template compiled into the image is used
(the same text).

1. Edit the template. Either change `files/default.html.tmpl` in your copy of the chart, or keep
   your own file and pass it with `--set-file emailTemplate.content=my.html` (or put it in your
   values file as a block scalar). An empty `emailTemplate.content` means the shipped default.
2. `helm upgrade` the release.
3. Tell cyclops to use it. Either set `emailTemplate.useAsDefault: true` (default `false`), which makes
   it the template for every `CertReport` without its own reference, or reference it from each
   `CertReport` that should use it:

```yaml
apiVersion: cyclops.sakshitposting-irl.github.io/v1alpha1
kind: CertReport
metadata:
  name: prod
spec:
  emailTemplateConfigMapRef:
    name: cyclops-email-template   # <release>-email-template, or emailTemplate.name
    key: template.html
  notifiers:
    - type: Email
      email:
        from: cyclops@example.com
        to: [platform@example.com]
```

With `useAsDefault: false`, editing the template changes only the ConfigMap; reports keep using the
template built into the image until a `CertReport` references the ConfigMap. A `CertReport`'s own
`emailTemplateConfigMapRef` always wins over the default. `useAsDefault: true` needs
`emailTemplate.create: true` (the install fails otherwise).

`files/default.html.tmpl` is a copy of `internal/render/default.html.tmpl`; `make helm-crd` re-syncs
it (maintainers: do not edit it in the repo, edit the source).

### What you can change

**Nothing in the template is required.** The whole file is the email body, sent as a single
`text/html` part (there is no plain-text alternative), so you can restyle, restructure, shorten or
replace all of it. The shipped file starts with a comment block (it produces no output) that lists the
available data, and marks the parts people usually edit (the empty branch, the table rows and the footer):

| Part of the default | Edit it to |
|---|---|
| Title and "Generated" line | Rename the heading, drop or reformat the timestamp |
| Empty branch (`{{ if not .Findings }}`) | Reword the "nothing needs attention" message |
| Table (`{{ range .Findings }}` and its header row) | Add, remove or reorder columns. Change the header row and the row body together |
| Footer paragraph | Edit or remove |

What is fixed:
- **The subject line** is set by cyclops, not the template.
- **The data** is only what is listed under "Template data" below. A template that names any other
  field fails the run.

Recommendations, not requirements:
- Keep styling **inline** (`style="..."`). Email clients ignore `<style>` blocks and scripts.
- Keep the **empty-findings branch**. A day with nothing to report must still render, and the
  welcome email planned in ADR 0015 will use the same template.
- Keep `.Reason` and `.State`, since they are the only place cert-manager's own diagnostics appear.

### Template language

Templates are Go [`html/template`](https://pkg.go.dev/html/template): every value is escaped for its
context, so text from cert-manager (which can come from an external CA) cannot inject markup. Only
the standard actions and functions are available (`if`, `range`, `with`, `len`, `eq`, `and`, `or`,
`printf`, ...); cyclops adds none. The **subject line is not templated**: it is set by cyclops
(`[cyclops] 3 certificates need attention`, or `[cyclops] All certificates healthy`).

If the ConfigMap or key is missing, or the template fails to parse or execute, the run fails and
no email is sent, and the `CertReport` shows `ReportSucceeding=False`. The template is only loaded on
a day when something needs attention, so a broken template can stay hidden until then. Test changes
on a non-production `CertReport` with a certificate that is reported.

### Template data

The root value (`.`) is:

| Field | Type | Meaning |
|---|---|---|
| `.ReportName` | string | The `CertReport`'s name |
| `.GeneratedAt` | `time.Time` (UTC) | When the report ran |
| `.Findings` | list of rows | Certificates that need attention, most urgent first. May be empty |

Each row in `.Findings`:

| Field | Type | Meaning |
|---|---|---|
| `.Kind` | string | Why it is listed: `NeverIssued`, `Expired` or `RenewalOverdue` |
| `.Namespace`, `.Name` | string | The cert-manager `Certificate` |
| `.NotAfter` | `time.Time` (UTC) | Expiry. Zero if never issued: test with `.NotAfter.IsZero` |
| `.DaysLeft` | int | Whole days until `NotAfter`, rounded down; negative once expired. Only meaningful when `NotAfter` is set |
| `.RenewalTime` | `time.Time` (UTC) | When cert-manager is due to renew. Zero if none scheduled |
| `.FailedIssuanceAttempts` | int | cert-manager's failed issuance count |
| `.Ingresses` | []string | Names of the annotated Ingresses that put the certificate in scope (annotation mode). Empty otherwise |
| `.State`, `.Reason` | string | cert-manager's own diagnostics, verbatim. Either may be empty. `Reason` is untrusted text |

Times are `time.Time`, so format them with `.NotAfter.Format "2006-01-02"`.

This is a public contract (ADR 0013): fields may be added in later versions, but none will be
renamed or removed, so a custom template keeps working across upgrades. Unknown fields are an
execution error, so do not reference fields not listed here.

A minimal template:

```html
<h2>{{ .ReportName }}: {{ len .Findings }} need attention</h2>
<ul>
{{- range .Findings }}
  <li>{{ .Namespace }}/{{ .Name }}: {{ .Kind }}{{ with .Reason }} ({{ . }}){{ end }}</li>
{{- end }}
</ul>
```

## Values

See [`values.yaml`](values.yaml) (commented) and [`values.schema.json`](values.schema.json), which
rejects bad combinations at `helm install`/`upgrade`: SMTP without a host, SES without a region, and
`tls: None` together with a credentials Secret.

| Key | Default | Meaning |
|---|---|---|
| `image.repository`, `image.tag` | `ghcr.io/sakshitposting-irl/cyclops`, chart `appVersion` | Controller and report image (same binary) |
| `email.provider` | `SMTP` | `SES` or `SMTP`; one per install |
| `email.smtp.*` | host required | `host`, `port` (default from `tls`), `tls` (`StartTLS`/`TLS`/`None`), `credentialsSecretName` |
| `email.ses.*` | region required | `region`, `credentialsSecretName` (empty = ambient AWS identity) |
| `reportServiceAccount.annotations` | `{}` | e.g. the IRSA role ARN |
| `emailTemplate.create` / `.name` / `.content` | `true` / derived / shipped default | The template ConfigMap above |
| `emailTemplate.useAsDefault` | `false` | Use that ConfigMap for every `CertReport` without its own reference |
| `crd.enabled` / `crd.keep` | `true` / `true` | Install the CRD; keep it (and every `CertReport`) on uninstall |
| `rbac.userRoles` | `true` | Admin/editor/viewer ClusterRoles for `CertReport` |
| `rbac.ingressAccess` | `true` | Let report Pods read Ingresses; needed only for `spec.ingressAnnotationMode` |
| `metrics.enabled` / `.port` | `true` / `8443` | HTTPS metrics endpoint |
| `controller.*` | | replicas, leader election, resources, scheduling |
