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

// Package certmanager converts cert-manager objects into report.CertStatus.
// It is the only package that imports cert-manager's API types, so the
// reporting logic in internal/report stays plain Go.
package certmanager

import (
	"fmt"
	"slices"
	"time"

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// healthyStates are ACME states that need no diagnostics. Anything else,
// including states cert-manager may add later, is relayed as-is.
var healthyStates = []acmev1.State{acmev1.Valid}

// ToCertStatus converts cert into cyclops's view of it. It makes no API
// calls: reqs, orders and chals are passed in, and may hold objects
// belonging to other certificates.
func ToCertStatus(
	cert *cmapi.Certificate,
	reqs []cmapi.CertificateRequest,
	orders []acmev1.Order,
	chals []acmev1.Challenge,
) report.CertStatus {
	s := report.CertStatus{
		Namespace:   cert.Namespace,
		Name:        cert.Name,
		NotAfter:    timeOrZero(cert.Status.NotAfter),
		RenewalTime: timeOrZero(cert.Status.RenewalTime),
	}
	var nullcheck_int = cert.Status.FailedIssuanceAttempts
	if nullcheck_int != nil {
		s.FailedIssuanceAttempts = *nullcheck_int
	}

	// Find the newest CertificateRequest owned by cert. Ownership is by owner
	// reference (UID), not name: requests are named "<cert>-<revision>".
	var newestReq *cmapi.CertificateRequest
	for i := range reqs {
		req := &reqs[i]
		if metav1.IsControlledBy(req, cert) {
			if newestReq == nil || req.CreationTimestamp.After(newestReq.CreationTimestamp.Time) {
				newestReq = req
			}
		}
	}

	if newestReq == nil {
		return s
	}

	// Find the Order owned by newestReq. Non-ACME issuers have none; for
	// them the request's own Ready condition is the diagnostic (below).
	var newestOrder *acmev1.Order
	for i := range orders {
		order := &orders[i]
		if metav1.IsControlledBy(order, newestReq) {
			if newestOrder == nil || order.CreationTimestamp.After(newestOrder.CreationTimestamp.Time) {
				newestOrder = order
			}
		}
	}

	if newestOrder == nil {
		s.State, s.Reason = requestNotReady(newestReq)
		return s
	}

	// A failing Challenge takes priority over the Order, since its Reason is
	// more specific. If several are failing, the first by name wins so the
	// report doesn't change from run to run.
	var failingChal *acmev1.Challenge
	for i := range chals {
		chal := &chals[i]
		if metav1.IsControlledBy(chal, newestOrder) && !slices.Contains(healthyStates, chal.Status.State) {
			if failingChal == nil || chal.Name < failingChal.Name {
				failingChal = chal
			}
		}
	}

	if failingChal != nil {
		s.State = string(failingChal.Status.State)
		s.Reason = failingChal.Status.Reason
		return s
	}

	if !slices.Contains(healthyStates, newestOrder.Status.State) {
		s.State = string(newestOrder.Status.State)
		s.Reason = newestOrder.Status.Reason
	}

	return s
}

// requestNotReady returns the Reason and Message of req's Ready condition
// when it is not True, verbatim (ADR 0004, ADR 0021). Empty when the request
// is ready or cert-manager hasn't set the condition yet.
func requestNotReady(req *cmapi.CertificateRequest) (state, reason string) {
	for _, c := range req.Status.Conditions {
		if c.Type == cmapi.CertificateRequestConditionReady && c.Status != cmmeta.ConditionTrue {
			return c.Reason, c.Message
		}
	}
	return "", ""
}

// IssuerError describes why cert's issuer is not ready, or returns "" when it
// is ready, was not found, or is an external issuer (a group other than
// cert-manager.io, whose type this package doesn't read). issuers and
// clusterIssuers may hold objects for other namespaces and names (ADR 0021).
func IssuerError(cert *cmapi.Certificate, issuers []cmapi.Issuer, clusterIssuers []cmapi.ClusterIssuer) string {
	ref := cert.Spec.IssuerRef
	if ref.Group != "" && ref.Group != cmapi.SchemeGroupVersion.Group {
		return ""
	}

	var kind string
	var conditions []cmapi.IssuerCondition
	switch ref.Kind {
	case "", cmapi.IssuerKind:
		kind = cmapi.IssuerKind
		i := slices.IndexFunc(issuers, func(i cmapi.Issuer) bool {
			return i.Namespace == cert.Namespace && i.Name == ref.Name
		})
		if i < 0 {
			return ""
		}
		conditions = issuers[i].Status.Conditions
	case cmapi.ClusterIssuerKind:
		kind = cmapi.ClusterIssuerKind
		i := slices.IndexFunc(clusterIssuers, func(i cmapi.ClusterIssuer) bool { return i.Name == ref.Name })
		if i < 0 {
			return ""
		}
		conditions = clusterIssuers[i].Status.Conditions
	default:
		return ""
	}

	for _, c := range conditions {
		if c.Type == cmapi.IssuerConditionReady && c.Status != cmmeta.ConditionTrue {
			return fmt.Sprintf("%s %s: %s: %s", kind, ref.Name, c.Reason, c.Message)
		}
	}
	return ""
}

// timeOrZero unwraps an optional API timestamp. cert-manager leaves these
// nil until they are known; report treats the zero time as "not set".
func timeOrZero(t *metav1.Time) time.Time {
	if t != nil {
		return t.Time
	}
	return time.Time{}
}
