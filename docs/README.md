# Cyclops documentation

| Folder | What's in it |
|---|---|
| [`decisions/`](decisions/README.md) | Architecture Decision Records: what's been decided (with pros/cons) and what's still open. Start here before changing the design. |
| [`internals/`](#internals) | How the code works: walkthroughs of the running pieces and the reasoning behind them. |

## Internals

| Doc | Covers |
|---|---|
| [manager-startup.md](internals/manager-startup.md) | `cmd/main.go`: the Scheme, `main`/`run`, flags, the controller-runtime Manager, startup timeline |
| [leader-election.md](internals/leader-election.md) | the Lease mechanism, timers, failover, and the open HA decision |
| [report-pipeline.md](internals/report-pipeline.md) | `List`, `CertStatus`, `ToCertStatus`, `Evaluate`, `render`: how certificates are read from the cluster, which are reported, where the diagnostics come from, and how the email is rendered |

Internals docs describe the code as it is. When the code changes, update the matching doc in the
same commit. Decisions go in `decisions/`, not here.
