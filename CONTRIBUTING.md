# Contributing to cyclops

Thanks for helping out. Cyclops is a small, alpha-stage project, so a few notes up front keep
things quick for everyone. By taking part you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Before you write code

**Design decisions are recorded, not improvised.** Every architectural decision lives in
[`docs/decisions/`](docs/decisions/README.md) as a numbered ADR with the options considered and
the reasoning. Read the index first. It lists what is decided and what is still open.

- If your change touches a decided area (data model, watch strategy, delivery, packaging...),
  follow the ADR. To change the decision, propose a new or amended ADR, with pros and cons, in
  your pull request or in an issue first.
- If it touches something listed under **Open / not yet decided**, please open an issue to
  discuss it rather than implementing around it.
- Small fixes, tests and docs improvements don't need an ADR.

For anything beyond a small fix, open an issue first so we agree on the approach before you spend
time on it.

## Reporting bugs and asking questions

Open a GitHub issue with:

- what you did and what you expected;
- what happened, with the relevant output (`kubectl describe certreport <name>` and the report
  Job's logs are the most useful);
- your cyclops version, Kubernetes version and cert-manager version.

Please don't paste credentials or Secret contents. For a security problem, don't file a public
issue: contact the maintainer privately through their GitHub profile.

## Development setup

Prerequisites: Go 1.26+, Docker, `kubectl`, and a [kind](https://kind.sigs.k8s.io) cluster with
cert-manager installed (only needed for cluster tests). `controller-gen`, `golangci-lint` and the
other tools are downloaded into `bin/` on demand. The [README](README.md#development) has the
full walkthrough.

```sh
make build         # build bin/manager
make test          # unit tests (envtest)
make lint          # golangci-lint (make lint-fix applies autofixes)
make test-cluster  # cluster validation against kind-cyclops (optional)
make help          # every target
```

Generated files are checked in, so regenerate them when you change what they come from:

- After editing `api/v1alpha1/*_types.go` or any `+kubebuilder` marker: `make manifests generate`.
- After that, or after editing `internal/render/default.html.tmpl`: `make helm-crd` to refresh the
  chart's CRD and template copies. Don't edit `charts/cyclops/templates/crd.yaml` or
  `charts/cyclops/files/default.html.tmpl` by hand.
- Chart changes: `make helm-lint`.

## Making a change

1. Fork, then branch from `main`.
2. Keep the change focused: one concern per pull request.
3. Add or update tests. Bug fixes should come with a test that fails without the fix.
4. Run `make test` and `make lint` and make sure both pass. CI runs the same checks.
5. Update the docs that your change makes stale: the README status table, the chart README, and
   the ADRs if you changed a decision.
6. Open a pull request against `main` and describe what changed and why. Link the issue or ADR.

### Code style

Match the surrounding code: naming, comment density and idiom. Comments explain why, and point to
the ADR that decided the behaviour where there is one. `golangci-lint` is the arbiter for
formatting and common mistakes.

### Commit messages

Use a short conventional prefix and an imperative summary, as in the existing history:
`feat(report): ...`, `fix(controller): ...`, `docs: ...`, `test: ...`. The body, if any, is at
most three bullets covering the new features and, if existing logic was reworked, which files were
touched.

## License

Cyclops is licensed under the [Apache License 2.0](LICENSE). By contributing, you agree that your
contributions are licensed under the same terms.
