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

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// Snapshot is everything report mode reads from the cluster in one run.
type Snapshot struct {
	Certificates []cmapi.Certificate
	Requests     []cmapi.CertificateRequest
	Orders       []acmev1.Order
	Challenges   []acmev1.Challenge
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
		statuses = append(statuses, ToCertStatus(&s.Certificates[i], s.Requests, s.Orders, s.Challenges))
	}
	return statuses
}
