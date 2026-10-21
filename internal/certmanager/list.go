/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package certmanager

import (
	"context"
	"maps"
	"slices"

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// Snapshot is everything report mode reads from the cluster in one run.
type Snapshot struct {
	Certificates []cmapi.Certificate
	Requests     []cmapi.CertificateRequest
	Orders       []acmev1.Order
	Challenges   []acmev1.Challenge

	// Ingresses maps each kept Certificate's secret to the Ingresses that use
	// it. Only set in annotation mode (ADR 0020).
	Ingresses map[SecretKey][]string
}

// List reads Certificates, CertificateRequests, Orders and Challenges from
// the given namespaces. Empty namespaces means all namespaces, read with one
// cluster-wide List per kind (ADR 0010). A namespace that doesn't exist
// contributes nothing; the controller reports it (ADR 0011).
func List(ctx context.Context, c client.Reader, namespaces []string) (Snapshot, error) {
	var snap Snapshot

	// "" is client.InNamespace's "all namespaces", so an empty request
	// becomes one cluster-wide pass.
	if len(namespaces) == 0 {
		namespaces = []string{""}
	} else {
		// Remove duplicates. The map only remembers what's been seen; the
		// order comes from walking the input, since ranging over a map is random.
		nsMap := make(map[string]struct{})
		unique := make([]string, 0, len(namespaces))
		for _, ns := range namespaces {
			if _, seen := nsMap[ns]; !seen {
				nsMap[ns] = struct{}{}
				unique = append(unique, ns)
			}
		}
		namespaces = unique
	}

	for _, ns := range namespaces {
		var certList cmapi.CertificateList
		if err := c.List(ctx, &certList, client.InNamespace(ns)); err != nil {
			return snap, err
		}
		snap.Certificates = append(snap.Certificates, certList.Items...)

		var reqList cmapi.CertificateRequestList
		if err := c.List(ctx, &reqList, client.InNamespace(ns)); err != nil {
			return snap, err
		}
		snap.Requests = append(snap.Requests, reqList.Items...)

		var orderList acmev1.OrderList
		if err := c.List(ctx, &orderList, client.InNamespace(ns)); err != nil {
			return snap, err
		}
		snap.Orders = append(snap.Orders, orderList.Items...)

		var challengeList acmev1.ChallengeList
		if err := c.List(ctx, &challengeList, client.InNamespace(ns)); err != nil {
			return snap, err
		}
		snap.Challenges = append(snap.Challenges, challengeList.Items...)
	}

	return snap, nil
}

// CertStatuses converts every Certificate in the snapshot, using the
// snapshot's requests, orders and challenges for diagnostics.
func (s Snapshot) CertStatuses() []report.CertStatus {
	statuses := make([]report.CertStatus, 0, len(s.Certificates))
	for i := range s.Certificates {
		status := ToCertStatus(&s.Certificates[i], s.Requests, s.Orders, s.Challenges)
		cert := &s.Certificates[i]
		if names := s.Ingresses[SecretKey{cert.Namespace, cert.Spec.SecretName}]; len(names) > 0 {
			status.Ingresses = slices.Sorted(slices.Values(names))
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// SecretKey identifies a TLS Secret by namespace and name. It is the join key
// between an Ingress's spec.tls[].secretName and Certificate.spec.secretName
// (ADR 0020).
type SecretKey struct{ Namespace, SecretName string }

// AnnotatedIngresses lists Ingresses cluster-wide and returns, for every
// Ingress annotated with AnnotationCRName == crName, its TLS secrets mapped to
// the names of the Ingresses that use them. Two Ingresses can share a
// secretName, so the value is a slice (ADR 0020).
func AnnotatedIngresses(ctx context.Context, c client.Reader, crName, annotationKey string) (map[SecretKey][]string, error) {
	var ingList networkingv1.IngressList
	if err := c.List(ctx, &ingList); err != nil {
		return nil, err
	}
	out := make(map[SecretKey][]string)
	for i := range ingList.Items {
		ing := &ingList.Items[i]
		if ing.Annotations[annotationKey] != crName {
			continue
		}
		for _, tls := range ing.Spec.TLS {
			if tls.SecretName == "" {
				continue
			}
			k := SecretKey{Namespace: ing.Namespace, SecretName: tls.SecretName}
			out[k] = append(out[k], ing.Name)
		}
	}
	return out, nil
}

// ListForIngresses is List for annotation mode (ADR 0020): the scope is the
// Certificates behind Ingresses annotated annotationKey == crName, found by
// secretName, instead of a list of namespaces.
func ListForIngresses(ctx context.Context, c client.Reader, crName, annotationKey string) (Snapshot, error) {
	secrets, err := AnnotatedIngresses(ctx, c, crName, annotationKey)
	var snapshot Snapshot
	if err != nil {
		return snapshot, err
	}
	if len(secrets) == 0 {
		return snapshot, nil
	}
	nsSet := make(map[string]struct{})
	for k := range secrets {
		nsSet[k.Namespace] = struct{}{}
	}
	namespaces := slices.Sorted(maps.Keys(nsSet))

	snapshot, err = List(ctx, c, namespaces)
	if err != nil {
		return snapshot, err
	}

	// Keep only the Certificates behind an annotated Ingress. Requests, Orders
	// and Challenges stay unfiltered; they are only looked up per kept
	// Certificate.
	snapshot.Certificates = slices.DeleteFunc(snapshot.Certificates, func(cert cmapi.Certificate) bool {
		_, ok := secrets[SecretKey{Namespace: cert.Namespace, SecretName: cert.Spec.SecretName}]
		return !ok
	})
	snapshot.Ingresses = secrets
	return snapshot, nil
}
