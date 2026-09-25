# Leader election

How controller-runtime makes sure only one replica of the manager reconciles at a time, and what it
means for cyclops. Enabled by the `--leader-elect` flag (on in `config/manager/manager.yaml`, off
for `make run`). See [manager-startup.md](manager-startup.md) for where it sits in startup.

The cyclops-specific decision is still open (HA / leader election in
[`../decisions/README.md`](../decisions/README.md)); the last section sketches the argument.

---

## The problem

Two replicas running the same controller **race**: both see a `CertReport` with no CronJob, both
`Create`, one gets `AlreadyExists`, both `Update` with different resourceVersions, status thrashes.
Harmless-ish for us; disastrous for controllers that create cloud resources.

"Run one replica" isn't the fix, because a node drain leaves zero. The fix: run N, exactly one
active, the rest hot-standby.

## The mechanism: a `Lease`

Kubernetes has no election API. It has compare-and-swap on any object via `resourceVersion`.
Leader election is built on that with one small object:

```yaml
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: d7c22753.sakshitposting-irl.github.io      # LeaderElectionID
  namespace: cyclops-system                        # the manager pod's namespace
spec:
  holderIdentity: cyclops-controller-manager-7d9f4-x2k8p_a1b2c3d4   # hostname + random
  leaseDurationSeconds: 15
  acquireTime: "2026-09-15T08:00:00.000000Z"
  renewTime:   "2026-09-15T08:04:32.000000Z"       # heartbeat
  leaseTransitions: 3
```

Every replica runs this loop:

```
every RetryPeriod (2s):
    GET the Lease
    if missing:                 → CREATE with me as holder   (CAS: fails if someone beat me)
    if holder == me:            → UPDATE renewTime = now     (heartbeat)
    if holder != me:
        if now > renewTime + leaseDurationSeconds:
                                → UPDATE holder = me         (CAS on resourceVersion; one wins)
        else                    → stay follower
```

Two followers grabbing an expired lease at the same time: the apiserver accepts one and rejects the
other with `409 Conflict`. That's the whole election: no Raft, just etcd CAS.

## The three timers

| Option | Default | Meaning |
|---|---|---|
| `RetryPeriod` | 2s | how often each replica runs the loop |
| `RenewDeadline` | 10s | how long the leader keeps *trying* to renew before concluding it lost |
| `LeaseDuration` | 15s | how stale `renewTime` must be before a follower may take over |

Invariant: `LeaseDuration > RenewDeadline > RetryPeriod`. A partitioned leader gives up at 10s,
*before* followers may take over at 15s, so there are never two leaders. The 5s gap is the safety
margin.

Failover timelines:

```
Hard crash (OOMKill, node dies):
  t=0      leader's last successful renew
  t=0..15  followers see renewTime fresh → wait
  t≈15–17  first follower whose tick lands after expiry grabs it
  ─────── ~15–17s of no reconciles

Graceful (SIGTERM) with ReleaseOnCancel: false   (scaffold default):
  same as hard crash — leader just stops renewing.

Graceful with ReleaseOnCancel: true:
  leader UPDATEs Lease: holder="", leaseDurationSeconds=1
  next follower tick (≤2s) grabs it.
  ─────── ~2s gap
```

`LeaderElectionReleaseOnCancel` (commented out in the `ctrl.Options` passed to `NewManager`) is safe
**only if nothing runs after `mgr.Start` returns**; otherwise you've announced "not leader" and kept
writing, which means two leaders. `run` returns right after `Start` and `main` exits, so enabling it
is safe.

## What controller-runtime does with it

Runnables are sorted by a tiny interface:

```go
type LeaderElectionRunnable interface {
    NeedLeaderElection() bool
}
```

| Runnable | `NeedLeaderElection()` | Runs on followers? |
|---|---|---|
| Controllers (our reconciler) | `true` | **no**: queued, informers not started |
| Metrics server | `false` | yes |
| Health probe server | `false` | yes |
| Webhook server | `false` | yes, webhooks must answer on every replica |

A follower pod is alive, `/readyz` green, metrics scraping, with **zero reconciles and zero cache**.

**Losing leadership** is handled brutally on purpose: if the leader fails to renew within
`RenewDeadline`, `mgr.Start` returns `leader election lost`, `main` exits 1, kubelet restarts the
container, and it rejoins as a follower. A stale cache and in-flight writes make "step down and
continue" unsafe, so it dies. This shows up as a restart count; it's expected, not a bug.

## Where it lives in the cluster

- **`LeaderElectionID`** → the Lease name. Random hex prefix so two projects sharing a domain don't
  collide.
- **Namespace** → the pod's own, from `/var/run/secrets/kubernetes.io/serviceaccount/namespace`.
  Outside a cluster that file doesn't exist, so `make run -- --leader-elect` fails with
  `unable to find leader election namespace` unless `LeaderElectionNamespace` is set. That's why the
  flag defaults to `false` and only the manifest turns it on.
- **RBAC** → `config/rbac/leader_election_role.yaml`: a namespaced `Role` with full CRUD on `leases`
  (`coordination.k8s.io`), `configmaps` (legacy lock type, kept for compatibility), and `events`
  create/patch (emits `LeaderElection` events on transitions:
  `kubectl get events --field-selector reason=LeaderElection`).
- **Replicas** → `config/manager/manager.yaml` has `replicas: 1`. Leader election *enabled* with
  *one replica* isn't pointless: bumping to 2 is a one-line change with no surprise, and it guards
  the rolling-update overlap where old and new pods briefly coexist.

## Experiment (once deployed to kind)

```bash
kubectl -n cyclops-system scale deploy cyclops-controller-manager --replicas=2
kubectl -n cyclops-system get lease -w          # holderIdentity + renewTime tick every 2s
kubectl -n cyclops-system delete pod <holder>   # kill the leader
# watch renewTime go stale, then holderIdentity flip and leaseTransitions++
kubectl -n cyclops-system get events --field-selector reason=LeaderElection
```

## What it means for cyclops (open decision)

Not decided yet. Shape of the argument:

**Manager (long-running pod):**
- After ADR 0006 its only job is keeping one CronJob in sync per `CertReport`. Cheap, rare.
- A 15s failover gap is irrelevant: the CronJob fires on schedule regardless. Kubernetes' own
  CronJob controller is what needs to be up, and that's the control plane's problem.
- Keeping it on costs a Lease, a Role and one arg, all scaffolded. Turning it off risks silent
  double-reconcile if anyone ever sets `replicas: 2`.

**Report Job (one-shot pod):**
- Must **not** use the manager or leader election. Single process: runs, exits. With
  `concurrencyPolicy: Forbid` on the CronJob, Kubernetes guarantees no overlap, which is the real
  dedup boundary for "did we send this email twice" (ties to the open "state & dedup" item).

Lean: keep leader election on, `replicas: 1`, enable `ReleaseOnCancel: true`, and record it as a
short ADR.
