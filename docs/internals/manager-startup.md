# Manager startup (`cmd/main.go`)

How the controller process starts: the Scheme, flags, the controller-runtime Manager, and what
happens between `kubelet starts pod` and our first `Reconcile`. Written against controller-runtime
v0.25.0 and apimachinery/client-go v0.37.0.

Leader election has its own page: [leader-election.md](leader-election.md).
Framework reference: the kubebuilder book (https://book.kubebuilder.io).

---

## The map

`main.go` does five things. Everything else is plumbing around them.

```
┌──────────────────────────────────────────────────────────────┐
│ cmd/main.go                                                  │
│                                                              │
│  ① init():  build the Scheme                                 │
│  ② main():  call run(), map its error to an exit code        │
│  ③ run():   parse flags + logger                             │
│             configure HTTP servers (metrics, webhook, probes)│
│  ④          ctrl.NewManager(...)                             │
│  ⑤          register reconciler, health checks, mgr.Start()  │
└──────────────────────────────────────────────────────────────┘
```

The HTTP server setup in ③ is the longest part and the least important for cyclops. ④ and ⑤
are the whole point.

```
declare knobs → bind flags → parse → logger
→ build tls tweaks → build webhook opts → build metrics opts
→ GetConfig → NewManager(cfg, opts)   # assemble, powered off
→ reconciler.SetupWithManager         # plug in our controller
→ health checks                       # plug in probes
→ Start(ctx)                          # power on, block until SIGTERM
```

---

## Imports

```go
_ "k8s.io/client-go/plugin/pkg/client/auth"
```

A blank import runs the package's `init()` even though no symbol is referenced. This one registers
cloud auth providers (GCP, Azure, OIDC, `exec` plugins) into client-go's auth registry. Without it,
a kubeconfig with `users[].user.exec: {command: aws eks get-token}` fails with "no Auth Provider
found". Irrelevant in-cluster (ServiceAccount token); matters for `make run` from a laptop against
EKS/GKE.

The rest are aliased so the code reads `ctrl.NewManager`, `metricsserver.Options`, etc.
`// +kubebuilder:scaffold:imports` is a marker, not code: `kubebuilder create api` inserts new
imports there.

---

## The Scheme: package-level vars and `init()`

```go
var (
    scheme   = runtime.NewScheme()
    setupLog = ctrl.Log.WithName("setup")
)

func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(cyclopsv1alpha1.AddToScheme(scheme))
    // +kubebuilder:scaffold:scheme
}
```

### Why a package-level `var`, not a local

`NewScheme()` is a constructor: every call makes a fresh, empty map. The point is to allocate
**once** and share **the same pointer**:

- `init()` fills it
- `ctrl.NewManager` receives it via `ctrl.Options{Scheme: scheme}`
- `mgr.GetScheme()` returns that same pointer to the reconciler

One map, three references. It has to be package-level because `init()` runs before `main()`, and
the only way `init()` can hand a value to `main()` is through a package-level variable.

Wrong version, for contrast:

```go
// three different schemes — the manager's is EMPTY
utilruntime.Must(cyclopsv1alpha1.AddToScheme(runtime.NewScheme()))        // filled, then discarded
mgr, _ := ctrl.NewManager(cfg, ctrl.Options{Scheme: runtime.NewScheme()}) // knows nothing
```

`setupLog` is the same idea in a milder form: `WithName` builds a child logger; build it once,
reuse. Every line it prints carries `"logger":"setup"`.

### What goes into the scheme

| Call | Registers |
|---|---|
| `clientgoscheme.AddToScheme` | every **built-in** Kubernetes type: Pod, Secret, ConfigMap, Deployment, CronJob, Job, Lease, … hundreds of GVKs |
| `cyclopsv1alpha1.AddToScheme` | `CertReport` and `CertReportList` under `cyclops.sakshitposting-irl.github.io/v1alpha1` |
| `// +kubebuilder:scaffold:scheme` | marker; the next `kubebuilder create api` inserts its `AddToScheme` here |

**Not registered yet:** cert-manager's types. The cert-manager module is now a dependency (used by
`internal/certmanager`), but until report mode adds `cmapi.AddToScheme(scheme)` and
`acmev1.AddToScheme(scheme)`, any `client.List(ctx, &cmapi.CertificateList{})` fails with
`no kind "CertificateList" is registered for version "cert-manager.io/v1"`.

`utilruntime.Must(err)` is `if err != nil { panic(err) }`. Panicking in `init()` is correct: no
scheme means no useful binary, and there's no caller to return an error to.

### "Why do we need the built-ins? kubectl already knows them."

The Scheme is an **in-memory Go map inside whichever process built it**. Every process builds its
own. The only thing that crosses process boundaries is JSON.

```
┌───────────────┐   ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│ kube-apiserver│   │    kubectl    │   │  cert-manager │   │    cyclops    │
│ its own Scheme│   │ its own Scheme│   │ its own Scheme│   │ its own Scheme│
└───────┬───────┘   └───────┬───────┘   └───────┬───────┘   └───────┬───────┘
        └───────────────────┴──── JSON over HTTPS ─────────────────┘
```

Our process needs the built-ins because our code touches them: `r.Create(ctx, &batchv1.CronJob{})`
(ADR 0006) has to look up the GVK of `*batchv1.CronJob` to stamp `apiVersion`/`kind`;
`Owns(&batchv1.CronJob{})` has to decode CronJob JSON into a struct; likewise the email-template
ConfigMap (ADR 0005) and the leader-election Lease.

Two separate things:
- **Type definitions** (`k8s.io/api/batch/v1.CronJob`) exist as Go structs once imported: compile time.
- **Registration** (GVK ↔ `reflect.Type`) is a runtime map entry that exists only after
  `AddKnownTypes` on a specific Scheme instance.

Having the struct compiled in does not tell the Scheme that the JSON string `"CronJob"` means that
struct.

### "Why a private scheme? Why not the global one and only add what's missing?"

That option exists and works. `k8s.io/client-go/kubernetes/scheme` exports a package-level,
pre-populated `Scheme`, and controller-runtime **defaults** to it when `Options.Scheme` is nil, with
the doc comment: *"Defaults to the kubernetes/scheme.Scheme, but it's almost always a good idea to
pass your own scheme in."*

The scaffold makes a private one for the same reason Go code avoids `http.DefaultServeMux`: a
package-level global is mutable by every package in the binary, including transitive dependencies
you never see, in an import order you don't control.

| Option | Pros | Cons |
|---|---|---|
| **A: private `runtime.NewScheme()`** (what we have) | immune to other libraries' `init()` side effects; `main.go` is the full inventory of decodable kinds; test isolation for free | one extra line |
| **B: `clientgoscheme.Scheme` global** | one fewer line | any transitive import can mutate it; "Double registration of different types" panics at runtime; controller-runtime docs advise against |

A costs one line and microseconds. Cyclops imports cert-manager's API module and later the AWS SDK,
a broad dependency tree. Keep A.

---

## `main()` and `run()`

```go
func main() {
    if err := run(ctrl.SetupSignalHandler(), os.Args[1:]); err != nil {
        setupLog.Error(err, "Manager exited with error")
        os.Exit(1)
    }
}

func run(ctx context.Context, args []string) error { ... }
```

`main` is the **only** place that turns an error into an exit code. Everything else lives in `run`,
which returns wrapped errors (`fmt.Errorf("creating manager: %w", err)`) instead of calling
`os.Exit`. Two reasons:

- `os.Exit` skips deferred cleanup, so calling it deep inside setup is a trap once anything is
  deferred.
- `run` can be tested. `cmd/main_test.go` calls it with bad args / no kubeconfig and checks the
  error, which is impossible if the function exits the process.

This is also where report mode (ADR 0006) will branch: `run` is the natural place to choose between
"start the manager" and "run one report and exit".

### Declare the knobs

Twelve locals, all zero-valued. All but `tlsOpts` are about to be bound to flags.
`tlsOpts []func(*tls.Config)` is a **slice of functions**: "here's how to tweak a TLS config"
callbacks. Empty until the HTTP/2 step.

### Bind flags

```go
fs := flag.NewFlagSet("cyclops", flag.ContinueOnError)
fs.StringVar(&metricsAddr, "metrics-bind-address", "0", "...")
```

Read as: *"when `--name=value` appears, write `value` into `x`; otherwise write the default."* The
`&` gives the flag package somewhere to write. Nothing is parsed yet, only registered.

Flags go into **our own `FlagSet`**, not the global `flag.CommandLine`. The global one can only be
parsed once per process and panics on duplicate registration, so tests could never call `run`
twice. `ContinueOnError` makes a bad flag return an error from `fs.Parse` instead of exiting.

| Flag | Default | Controls | Matters to cyclops? |
|---|---|---|---|
| `--metrics-bind-address` | `"0"` (off) | Prometheus `/metrics` listener | observability (open ADR item) |
| `--health-probe-bind-address` | `:8081` | `/healthz`, `/readyz` for kubelet | yes |
| `--leader-elect` | `false` | Lease-based single-active-replica | yes (open ADR item) |
| `--metrics-secure` | `true` | require SA token to scrape | somewhat |
| `--webhook-port` | `9443` | admission webhook listener | **no**, we have no webhooks |
| `--webhook-cert-*`, `--metrics-cert-*` | `""`, `tls.crt`, `tls.key` | on-disk TLS certs for those servers | no / somewhat |
| `--enable-http2` | `false` | keep HTTP/2 off (Rapid Reset CVE) | only affects the two servers |

What the pod actually gets (`config/manager/manager.yaml`):

```yaml
args:
  - --leader-elect
  - --health-probe-bind-address=:8081
```

So in-cluster: leader election on, probes on 8081, metrics off (the `config/default` kustomize layer
re-adds `--metrics-bind-address=:8443` if enabled there). `make run` locally: no args → leader
election off, which is what you want on a laptop.

### Logger

```go
opts := zap.Options{Development: true}
opts.BindFlags(fs)
if err := fs.Parse(args); err != nil { ... }
ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
```

1. Development mode: console encoder (human-readable), debug level, stack traces on `Error()`.
   Production is JSON + info. **This is the one default a real controller flips** (`false`, pass
   `--zap-devel` locally).
2. `BindFlags` adds `--zap-devel`, `--zap-log-level`, `--zap-encoder`, `--zap-stacktrace-level` to
   the same flag set.
3. **`fs.Parse(args)`** is the moment every variable gets its value. Before: registration. After:
   values readable.
4. Install as controller-runtime's **global** logger. `ctrl.Log`, `setupLog`, and
   `logf.FromContext(ctx)` in the reconciler all flow through this. controller-runtime buffers
   anything logged before `SetLogger` and flushes once it's set.

### HTTP/2 tweak

```go
disableHTTP2 := func(c *tls.Config) {
    c.NextProtos = []string{"http/1.1"}
}
if !enableHTTP2 {
    tlsOpts = append(tlsOpts, disableHTTP2)
}
```

A closure that restricts ALPN to HTTP/1.1. Not called here, only appended to `tlsOpts`. The metrics
and webhook servers each build their own `tls.Config` later and run every function in `tlsOpts`
over it. This is the **functional options** idiom: pass mutators, not finished configs. It appears
again in `client.Options`, `zap.Opts`, etc.

### Webhook server

Builds (doesn't start) an HTTPS server for admission webhooks. If `--webhook-cert-path` is given,
it points at those files; otherwise controller-runtime self-signs at startup. **We register zero
webhooks**, so this listens on 9443 and answers nothing. Scaffold weight; candidate for
`--webhook-port=-1` or removal.

### Metrics server

```go
metricsServerOptions := metricsserver.Options{
    BindAddress:   metricsAddr,
    SecureServing: secureMetrics,
    TLSOpts:       tlsOpts,
}
if secureMetrics {
    metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
}
```

Same shape as the webhook block, but an *options struct*, not a built server: the manager builds
the metrics server itself. `FilterProvider` is middleware that, per `/metrics` request, calls the
API server twice: **TokenReview** (is this bearer token a real ServiceAccount?) and
**SubjectAccessReview** (may that SA `get` the non-resource URL `/metrics`?). That's why
`test/e2e` creates a ClusterRoleBinding, mints a token via TokenRequest, and curls with
`Authorization: Bearer`. Without a cert dir, a self-signed cert is generated (Prometheus would need
`insecure_skip_verify`).

---

## Building the Manager

```go
restConfig, err := ctrl.GetConfig()
if err != nil {
    return fmt.Errorf("loading kubeconfig: %w", err)
}

mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
    Scheme:                 scheme,
    Metrics:                metricsServerOptions,
    WebhookServer:          webhookServer,
    HealthProbeBindAddress: probeAddr,
    LeaderElection:         enableLeaderElection,
    LeaderElectionID:       "d7c22753.sakshitposting-irl.github.io",
})
```

Everything above was preparing arguments for this call.

**`ctrl.GetConfig()`** returns a `*rest.Config` (host, CA, credentials). Resolution order:
1. `--kubeconfig` flag
2. `$KUBECONFIG`
3. in-cluster: `/var/run/secrets/kubernetes.io/serviceaccount/{token,ca.crt}` + `KUBERNETES_SERVICE_HOST`
4. `~/.kube/config`

The scaffold used `GetConfigOrDie()`, which calls `os.Exit(1)` itself on failure. We use
`GetConfig()` so the error goes back through `main`, the single exit point.

**What a Manager is**: the process-level container that owns every shared thing.

```
                    ┌──────────────── Manager ────────────────┐
  rest.Config ────▶ │  RESTMapper  (GVK→GVR via discovery)     │
  Scheme ─────────▶ │  Cache       (one informer per watched   │
                    │               GVK, shared by everyone)   │
                    │  Client      (reads → cache, writes →    │
                    │               API server)                │
                    │  Controllers [certreport, ...]           │
                    │  Runnables   [metrics srv, probe srv,    │
                    │               webhook srv]               │
                    │  Leader election (Lease)                 │
                    │  Signal/lifecycle                        │
                    └──────────────────────────────────────────┘
```

The reason to go through the manager instead of building a client yourself: **the cache is
shared**. Two controllers interested in `CronJob` → one watch, one in-memory store.

`NewManager` does discovery to build the RESTMapper, creates the cache (informers not started),
creates the cache-backed client, wires the three HTTP servers as runnables, and prepares leader
election. **Nothing is running yet.** A fully assembled machine with the power off.

---

## Attaching our controller

```go
if err := (&controller.CertReportReconciler{
    Client: mgr.GetClient(),
    Scheme: mgr.GetScheme(),
}).SetupWithManager(mgr); err != nil {
```

The reconciler struct (`internal/controller/certreport_controller.go`):

```go
type CertReportReconciler struct {
    client.Client          // embedded interface → r.Get, r.List, r.Create promoted
    Scheme *runtime.Scheme
}
```

- `mgr.GetClient()` is the **cache-backed** client: `Get`/`List` are answered from the informer's
  in-memory store (fast, eventually consistent); `Create`/`Update`/`Patch`/`Delete` go to the API
  server. `mgr.GetAPIReader()` bypasses the cache if ever needed (rare).
- `mgr.GetScheme()` is the same pointer from `init()`. Needed for
  `controllerutil.SetControllerReference(certReport, cronJob, r.Scheme)`, which calls
  `scheme.ObjectKinds(certReport)` to know what `apiVersion`/`kind` to write into the owner
  reference (ADR 0006).
- `*CertReportReconciler` satisfies `reconcile.Reconciler` (one method:
  `Reconcile(ctx, Request) (Result, error)`) structurally; nothing is declared.

`SetupWithManager` → builder → `Complete(r)` does:
1. `For(&CertReport{})` → `scheme.ObjectKinds` → GVK
2. GVK → RESTMapper → GVR `certreports`, scope Cluster (a missing CRD shows up here as
   `no matches for kind "CertReport"`)
3. ask the cache for an informer on that GVR
4. register a handler: any add/update/delete → enqueue `reconcile.Request{NamespacedName}`
5. add the controller as a runnable (`MaxConcurrentReconciles: 1` default)

Still nothing running. `// +kubebuilder:scaffold:builder` is the insertion marker for the next
reconciler.

Later, for ADR 0006: `For(&CertReport{}).Owns(&batchv1.CronJob{})` adds a CronJob informer whose
handler reads the owner reference and enqueues the parent `CertReport`. That's what makes "someone
deleted the CronJob → controller recreates it" work.

---

## Health endpoints

```go
mgr.AddHealthzCheck("healthz", healthz.Ping)
mgr.AddReadyzCheck("readyz", healthz.Ping)
```

`healthz.Checker` is `func(*http.Request) error`; `Ping` always returns nil. Both endpoints on
`:8081` mean "process up and serving HTTP", nothing smarter. The manifest's liveness/readiness
probes hit these. Webhook-serving controllers use `mgr.GetWebhookServer().StartedChecker()`; we
don't need it.

---

## Turning it on

```go
// in main
run(ctrl.SetupSignalHandler(), os.Args[1:])

// in run
if err := mgr.Start(ctx); err != nil {
    return fmt.Errorf("running manager: %w", err)
}
```

`SetupSignalHandler()` returns a `context.Context` cancelled on the first `SIGTERM`/`SIGINT`; a
second signal `os.Exit(1)`s. Kubernetes sends `SIGTERM` on pod deletion, then `SIGKILL` after
`terminationGracePeriodSeconds`. `main` creates the context and passes it into `run`, so tests can
pass their own.

`mgr.Start(ctx)` **blocks for the life of the process.** Inside, in order:

```
1. start non-leader-election runnables   (probes → /readyz green; metrics; webhook)
2. if leader election: block until we hold the Lease   (followers stop here)
3. start the cache → LIST every watched GVR → open WATCH → wait for sync
4. start controllers → workers pull from queues, call Reconcile
   (the initial LIST already enqueued every existing CertReport → first reconciles fire now)
5. block until ctx is cancelled
6. on cancel: stop taking work, wait ≤ GracefulShutdownTimeout (30s) for in-flight
   Reconciles, stop informers, return
```

Returns `nil` → `run` returns nil → exit 0. Returns an error (`leader election lost`, a runnable
crashed) → `main` logs it and exits 1.

---

## Runtime timeline, all together

```
kubelet starts pod
  └─ Go runtime: run all init()s
       ├─ client-go auth plugins register themselves
       ├─ api/v1alpha1: SchemeBuilder gets its two funcs queued
       └─ cmd/main.go init(): scheme knows ~400 built-in GVKs + CertReport(+List)
  └─ main() → run(ctx, args)
       ├─ flags: --leader-elect --health-probe-bind-address=:8081
       ├─ logger set
       ├─ GetConfig → in-cluster SA token
       ├─ NewManager → RESTMapper discovery (GET /apis, /apis/cyclops.../v1alpha1)
       ├─ SetupWithManager → CertReport informer requested, controller queued
       ├─ health checks wired
       └─ Start
            ├─ :8081 up (kubelet probes go green)
            ├─ acquire Lease "d7c22753.sakshitposting-irl.github.io"
            ├─ LIST certreports → cache primed → Add events → queue: [daily-report]
            ├─ worker: Reconcile(ctx, {Name: "daily-report"})   ← our code, currently a no-op
            └─ WATCH certreports … forever, until SIGTERM
```

---

## Still to change (not done yet)

ADR 0006: one binary, two modes. `main.go` has one so far.

- Branch on a mode inside `run` (`args[0] == "report"` or a `--mode` flag). In report mode skip the
  manager: a plain `client.New(cfg, client.Options{Scheme: scheme})` (no cache, since it's one-shot),
  list Certificates, convert (`internal/certmanager`), evaluate (`internal/report`), render, send,
  exit 0/1. See [report-pipeline.md](report-pipeline.md).
- Register cert-manager's `certmanager/v1` and `acme/v1` types in `init()`.
- `Owns(&batchv1.CronJob{})` in `SetupWithManager`.
- Drop or disable the webhook server block.
- Probably `Development: false` on the zap options; `LeaderElectionReleaseOnCancel: true`.

---

## Glossary

| Term | One line |
|---|---|
| Scheme | per-process map GVK ⇄ Go type; lets JSON become structs and back |
| Manager | container for config, RESTMapper, cache, client, controllers, servers, leader election, lifecycle |
| RESTMapper | GVK → GVR + scope, learned from API server discovery |
| Cache / informer | one LIST+WATCH per GVK, in-memory store, shared; `Get`/`List` read from it |
| Runnable | anything the manager starts/stops: controllers, HTTP servers |
| Reconciler | `Reconcile(ctx, Request) (Result, error)`; our struct satisfies it structurally |
| Lease | `coordination.k8s.io/v1` object used as the leader-election lock via resourceVersion CAS |
| Functional options | pass a list of `func(*Config)` mutators instead of a finished config |
| `Must` / `OrDie` | panic / exit on error at startup, because there's nothing to recover to |
