# 1. Controller scaffolding framework

## Status
Decided

## Context
cyclops needs to run as a Kubernetes controller managing a custom resource.
The main choices for building a Go-based controller are controller-runtime
(kubebuilder), Operator SDK, or raw client-go/informers.

## Options considered

**controller-runtime + kubebuilder**
- Pros: industry-standard (the same library cert-manager itself is built
  on); scaffolds CRD types, RBAC markers, manager/cache/informer wiring for
  free; large ecosystem/docs; easy CRD versioning via kubebuilder markers.
- Cons: some generated boilerplate (deepcopy, CRD YAML from markers) that's
  opaque until you've worked with it; slightly heavier initial learning
  curve.

**Operator SDK**
- Pros: adds scaffolding on top of controller-runtime (Go operators under
  the hood are controller-runtime); more generators; OLM packaging support.
- Cons: extra abstraction/CLI dependency for OLM/Helm/Ansible-operator
  features that aren't needed for a single reporting controller.

**Raw client-go + informers**
- Pros: full transparency, zero generated code, minimal dependencies.
- Cons: would mean hand-rolling the informer cache, workqueue, leader
  election, CRD registration, and deepcopy — reinventing controller-runtime
  with more surface for subtle bugs (e.g. informer resync races).

## Decision
Use **controller-runtime with kubebuilder scaffolding**. Operator SDK's
extra value (OLM packaging) isn't relevant here, and raw client-go is a lot
of undifferentiated plumbing for no real gain. This also keeps us
consistent with the ecosystem we're watching, since cert-manager is built
on controller-runtime too.
