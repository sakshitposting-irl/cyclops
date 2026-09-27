# 14. Email delivery settings and credentials

## Status
Decided. Supersedes the per-notifier `provider: SES | SMTP` part of
[ADR 0008](0008-email-providers.md): the provider is now chosen at install
time, not per notifier. Depends on packaging cyclops as a Helm chart, which
is recorded separately (ADR 0016, open).

## Context
ADR 0008 put both SES and SMTP in v1 and left their credentials open. The
two providers authenticate differently:

- **SES**: AWS credentials, either ambient (IRSA or EKS Pod Identity on
  EKS, an instance role on EC2) or static access keys.
- **SMTP**: host, port, TLS mode, and optionally a username and password.

Some constraints come from earlier decisions:

- `CertReport` is cluster-scoped (ADR 0002), so a Secret reference can't
  default to "the CR's namespace". There isn't one.
- The report runs in a Job Pod created from a CronJob the controller owns
  (ADR 0006). A Pod can only mount Secrets or ConfigMaps from its own
  namespace.

## Options considered

### 1. Where delivery settings are configured

**A. Per notifier, inline in the `CertReport` spec**
- Pros: the CRD schema validates settings at apply time; different reports
  can use different servers or regions.
- Cons: the same server settings are copied into every `CertReport`. A
  cluster-scoped CR has no namespace, so it has to point at Secrets in some
  fixed namespace anyway.

**B. Once per install, in Helm `values.yaml`, rendered into a ConfigMap**
- Pros: mail delivery is set up once, where the operator installs cyclops,
  next to the ServiceAccount that carries the IRSA annotation. A
  `CertReport` only says who gets the email. `values.schema.json`
  validates the settings at `helm install`/`upgrade`.
- Cons: every `CertReport` in the cluster shares one mail server or SES
  region. Anyone who installs cyclops without Helm gets no schema
  validation.

### 2. Where the credential Secrets live

**A. A reference that includes a namespace**
- Cons: a Pod can't mount another namespace's Secret, so the report needs
  cluster-wide Secret read access, or has to run in that namespace.

**B. The namespace cyclops is installed in**
- Pros: the Job Pod reads the Secret directly, and no one needs Secret
  access outside that namespace. cert-manager uses the same rule: Secrets
  referenced by a `ClusterIssuer` must be in its "cluster resource
  namespace".
- Cons: setting up credentials needs write access to that namespace. That
  is fine, since installing cyclops needs it anyway.

### 3. How credentials reach the report Pod

**A. Report mode reads the Secret through the API**
- Cons: the report ServiceAccount needs `get` on Secrets.

**B. Env vars wired into the CronJob's Pod template (`secretKeyRef`)**
- Pros: the kubelet reads the Secret, so the report ServiceAccount needs
  no Secret access. The AWS SDK reads `AWS_ACCESS_KEY_ID` and
  `AWS_SECRET_ACCESS_KEY` natively. There's one set of credentials per
  install, so the variables don't need per-notifier indexes.

**C. Secret mounted as a volume**
- Pros: same RBAC benefit as B.
- Cons: report mode has to read files for something the AWS SDK already
  reads from the environment. It would only win if there were several
  credential sets per Pod, and there aren't (see 1B).

### 4. Secret keys

**A. Fixed key names**
- Pros: the smallest API.
- Cons: an existing Secret with other key names (for example one synced by
  external-secrets) has to be reshaped.

**B. A key selector per field** (as in cert-manager's Route53 solver)
- Cons: more fields for a need nobody has raised. Can be added later as
  optional overrides of the fixed names.

### 5. SES authentication

**A. Ambient AWS credential chain only**
- Cons: can't be used from clusters outside AWS that send through SES.

**B. Static keys only**
- Cons: forces long-lived keys on EKS users, who have a better option.

**C. Ambient by default, optional static-key Secret**
- Pros: covers both cases, and the default is the more secure path.

### 6. ServiceAccount for the report Job

**A. The controller's own ServiceAccount**
- Cons: the Job gets the controller's write access to CronJobs and
  `CertReport` status.

**B. A fixed `cyclops-report` ServiceAccount**
- Pros: least privilege; one place for the IRSA annotation.
- Cons: all reports share one AWS identity. This follows from 1B anyway.

**C. `spec.serviceAccountName` per `CertReport`**
- Cons: each ServiceAccount needs the ADR 0010 ClusterRole bound by hand,
  and with install-level delivery there's nothing per report to authorise.

### 7. SMTP TLS

The three modes:

- `TLS` (port 465): encrypted from the first byte.
- `StartTLS` (port 587): starts in plaintext and upgrades once the server
  announces STARTTLS, before the password is sent.
- `None` (port 25): never encrypted.

**A. Opportunistic STARTTLS**
- If the server doesn't announce STARTTLS, carry on in plaintext.
- Cons: a misconfigured server, or an attacker removing the announcement
  ("STARTTLS stripping"), makes cyclops send the password in cleartext,
  and nothing fails, so nobody notices.

**B. Strict STARTTLS**
- If the server doesn't announce STARTTLS, the send fails with a clear
  error.
- Pros: fails loudly, which is the right behaviour for a backstop.

**`insecureSkipVerify`**: accepts any certificate, including an
impostor's. The safe fix for self-signed relays is supplying their CA
certificate, which can be added later if needed.

### 8. A referenced Secret is missing

The Pod sticks in `CreateContainerConfigError`, the Job never runs, and
the report silently never arrives.

**A. Rely on Job/Pod status**
- Cons: invisible unless someone watches Pods in the cyclops namespace.

**B. A controller condition, like `NamespacesFound` (ADR 0011)**
- Pros: caught when the `CertReport` is applied or the Secret changes, not
  at the next scheduled run.
- Cons: the controller needs `get`, `list` and `watch` on Secrets in its
  own namespace.

## Decision

1. **Install-level configuration (1B).** Delivery settings are Helm
   values, rendered into a ConfigMap in the cyclops namespace and
   validated by `values.schema.json`. A `CertReport`'s Email notifier
   holds only `from` and `to`.
2. **Credential Secrets live in the cyclops namespace (2B).** Users
   create them by hand; the chart only references them by name. The
   values file carries a comment saying not to put credentials in it.
3. **Env vars (3B).**
   - The chart passes the ConfigMap and Secret names to the controller.
   - The controller adds `envFrom` for the ConfigMap and `secretKeyRef`
     env vars for the credentials to the CronJob's Pod template.
   - Report mode reads only its environment.
4. **Fixed key names (4A):**
   - SMTP: `username`, `password` → `CYCLOPS_SMTP_USERNAME`,
     `CYCLOPS_SMTP_PASSWORD`
   - SES: `access-key-id`, `secret-access-key` → `AWS_ACCESS_KEY_ID`,
     `AWS_SECRET_ACCESS_KEY`
5. **SES: ambient chain by default, optional static keys (5C).**
   - With no Secret configured, no AWS env vars are set, and the SDK's
     default chain finds IRSA or Pod Identity credentials.
   - `region` is required: off EC2 there's no reliable ambient region.
6. **Fixed `cyclops-report` ServiceAccount (6B).** It's bound to the ADR
   0010 read-only ClusterRole, and its annotations (e.g. the IRSA role ARN)
   are set through values.
7. **Strict STARTTLS by default (7B).**
   - `tls: StartTLS | TLS | None`, default `StartTLS`.
   - `port` defaults from the mode: 587, 465, or 25.
   - Credentials are optional; with none configured, the send uses no
     `AUTH`.
   - `values.schema.json` rejects `tls: None` together with a credentials
     Secret.
   - No `insecureSkipVerify` in v1.
8. **`CredentialsFound` condition (8B).**
   - On every `CertReport`, the controller checks that the configured
     Secret exists and has the expected keys.
   - If not, it sets `CredentialsFound=False` (reason `SecretNotFound` or
     `SecretKeyMissing`) and emits a Warning Event.
   - It watches Secrets in its own namespace only, with the cache limited
     to that namespace, so fixing the Secret clears the condition.

### Illustrative values

```yaml
email:
  provider: SMTP            # SES | SMTP
  # Do not put credentials here. Create the Secret yourself in the
  # release namespace and reference it by name.
  smtp:
    host: smtp.example.com
    port: 587               # optional; defaults from tls
    tls: StartTLS           # StartTLS | TLS | None
    credentialsSecretName: cyclops-smtp   # keys: username, password
  ses:
    region: eu-west-1
    credentialsSecretName: ""             # empty = IRSA / Pod Identity
                                          # keys: access-key-id, secret-access-key
reportServiceAccount:
  annotations: {}           # e.g. eks.amazonaws.com/role-arn: arn:aws:iam::...
```

```yaml
# CertReport
spec:
  notifiers:
    - type: Email
      email:
        from: cyclops@example.com
        to: [platform@example.com]
```

## RBAC changes (relative to ADRs 0010 and 0011)
- New `cyclops-report` ServiceAccount, bound to the ADR 0010 read-only
  ClusterRole.
- Controller: a Role in its own namespace with `get`, `list` and `watch` on
  `secrets` (the `CredentialsFound` check). It only references the
  ConfigMap by name and never reads it.
- Report Job: no Secret or ConfigMap access; the kubelet injects both.
