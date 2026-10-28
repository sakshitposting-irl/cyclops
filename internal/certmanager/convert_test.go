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
	"reflect"
	"testing"
	"time"

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/sakshitposting-irl/cyclops/internal/report"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

const testNamespace = "default"

// meta builds ObjectMeta with a UID (what owner references point at) and,
// if owner is non-nil, a controller owner reference to it.
func meta(name, uid string, created time.Time, owner metav1.Object) metav1.ObjectMeta {
	m := metav1.ObjectMeta{
		Namespace:         testNamespace,
		Name:              name,
		UID:               types.UID(uid),
		CreationTimestamp: metav1.NewTime(created),
	}
	if owner != nil {
		isController := true
		m.OwnerReferences = []metav1.OwnerReference{{
			Name: owner.GetName(), UID: owner.GetUID(), Controller: &isController,
		}}
	}
	return m
}

func certificate() *cmapi.Certificate {
	notAfter := metav1.NewTime(t0.Add(10 * 24 * time.Hour))
	renewal := metav1.NewTime(t0.Add(-20 * 24 * time.Hour))
	attempts := 3
	return &cmapi.Certificate{
		ObjectMeta: meta("web", "cert-uid", t0, nil),
		Status: cmapi.CertificateStatus{
			NotAfter:               &notAfter,
			RenewalTime:            &renewal,
			FailedIssuanceAttempts: &attempts,
		},
	}
}

func request(name, uid string, created time.Time, owner metav1.Object) cmapi.CertificateRequest {
	return cmapi.CertificateRequest{ObjectMeta: meta(name, uid, created, owner)}
}

// notReady returns req with a Ready=False condition, as cert-manager sets on a
// request that is waiting on, or refused by, its issuer.
func notReady(req cmapi.CertificateRequest, reason, message string) cmapi.CertificateRequest {
	req.Status.Conditions = []cmapi.CertificateRequestCondition{{
		Type: cmapi.CertificateRequestConditionReady, Status: cmmeta.ConditionFalse,
		Reason: reason, Message: message,
	}}
	return req
}

func order(name, uid string, owner metav1.Object, state acmev1.State, reason string) acmev1.Order {
	return acmev1.Order{
		ObjectMeta: meta(name, uid, t0, owner),
		Status:     acmev1.OrderStatus{State: state, Reason: reason},
	}
}

func challenge(name string, owner metav1.Object, state acmev1.State, reason string) acmev1.Challenge {
	return acmev1.Challenge{
		ObjectMeta: meta(name, name+"-uid", t0, owner),
		Status:     acmev1.ChallengeStatus{State: state, Reason: reason},
	}
}

// base is what every test expects from certificate() before diagnostics.
func base() report.CertStatus {
	return report.CertStatus{
		Namespace:              testNamespace,
		Name:                   "web",
		NotAfter:               t0.Add(10 * 24 * time.Hour),
		RenewalTime:            t0.Add(-20 * 24 * time.Hour),
		FailedIssuanceAttempts: 3,
	}
}

func withDiag(state, reason string) report.CertStatus {
	s := base()
	s.State, s.Reason = state, reason
	return s
}

func TestToCertStatus(t *testing.T) {
	cert := certificate()
	req := request("web-1", "req-uid", t0, cert)
	ord := order("web-1-order", "order-uid", &req, acmev1.Pending, "")

	// A second certificate's chain, which must never leak into cert's result.
	other := &cmapi.Certificate{ObjectMeta: meta("other", "other-uid", t0, nil)}
	otherReq := request("other-1", "other-req-uid", t0.Add(time.Hour), other)
	otherOrd := order("other-1-order", "other-order-uid", &otherReq, acmev1.Errored, "other's problem")

	tests := []struct {
		name   string
		reqs   []cmapi.CertificateRequest
		orders []acmev1.Order
		chals  []acmev1.Challenge
		want   report.CertStatus
	}{
		{
			name: "no requests: dates and attempts only",
			want: base(),
		},
		{
			// Non-ACME issuers (CA, Vault, self-signed...) never create Orders.
			name: "request without order: no diagnostics",
			reqs: []cmapi.CertificateRequest{req},
			want: base(),
		},
		{
			// A CA issuer whose Secret is missing: cert-manager never fails the
			// request, it just waits (ADR 0021).
			name: "request without order, not ready: the request's reason and message",
			reqs: []cmapi.CertificateRequest{notReady(req, "Pending", "Referenced issuer does not have a Ready status condition")},
			want: withDiag("Pending", "Referenced issuer does not have a Ready status condition"),
		},
		{
			name: "request without order, ready: no diagnostics",
			reqs: func() []cmapi.CertificateRequest {
				ready := req
				ready.Status.Conditions = []cmapi.CertificateRequestCondition{{
					Type: cmapi.CertificateRequestConditionReady, Status: cmmeta.ConditionTrue, Reason: "Issued",
				}}
				return []cmapi.CertificateRequest{ready}
			}(),
			want: base(),
		},
		{
			// With an Order, the ACME diagnostics are more specific than the request's.
			name:   "order wins over the request's condition",
			reqs:   []cmapi.CertificateRequest{notReady(req, "Pending", "Waiting on certificate issuance from order")},
			orders: []acmev1.Order{order("web-1-order", "order-uid", &req, acmev1.Invalid, "order failed")},
			want:   withDiag("invalid", "order failed"),
		},
		{
			name:   "order not valid, no challenges: order's state and reason",
			reqs:   []cmapi.CertificateRequest{req},
			orders: []acmev1.Order{order("web-1-order", "order-uid", &req, acmev1.Invalid, "order failed")},
			want:   withDiag("invalid", "order failed"),
		},
		{
			// The most common stuck case: the challenge is still pending but
			// its reason already says why the self-check keeps failing.
			name:   "pending challenge wins over order",
			reqs:   []cmapi.CertificateRequest{req},
			orders: []acmev1.Order{ord},
			chals: []acmev1.Challenge{
				challenge("web-1-chal", &ord, acmev1.Pending, "Waiting for HTTP-01 challenge propagation: connection refused"),
			},
			want: withDiag("pending", "Waiting for HTTP-01 challenge propagation: connection refused"),
		},
		{
			name:   "valid challenge falls back to order",
			reqs:   []cmapi.CertificateRequest{req},
			orders: []acmev1.Order{order("web-1-order", "order-uid", &req, acmev1.Errored, "finalize failed")},
			chals:  []acmev1.Challenge{challenge("web-1-chal", &ord, acmev1.Valid, "")},
			want:   withDiag("errored", "finalize failed"),
		},
		{
			name:   "everything valid: no diagnostics",
			reqs:   []cmapi.CertificateRequest{req},
			orders: []acmev1.Order{order("web-1-order", "order-uid", &req, acmev1.Valid, "")},
			chals:  []acmev1.Challenge{challenge("web-1-chal", &ord, acmev1.Valid, "")},
			want:   base(),
		},
		{
			name:   "several failing challenges: first by name",
			reqs:   []cmapi.CertificateRequest{req},
			orders: []acmev1.Order{ord},
			chals: []acmev1.Challenge{
				challenge("web-1-chal-b", &ord, acmev1.Invalid, "b failed"),
				challenge("web-1-chal-a", &ord, acmev1.Invalid, "a failed"),
			},
			want: withDiag("invalid", "a failed"),
		},
		{
			// An old failed attempt doesn't describe the current one.
			name: "newest request wins",
			reqs: func() []cmapi.CertificateRequest {
				old := request("web-0", "old-req-uid", t0.Add(-time.Hour), cert)
				return []cmapi.CertificateRequest{req, old}
			}(),
			orders: func() []acmev1.Order {
				old := request("web-0", "old-req-uid", t0.Add(-time.Hour), cert)
				return []acmev1.Order{
					order("web-0-order", "old-order-uid", &old, acmev1.Errored, "old failure"),
					order("web-1-order", "order-uid", &req, acmev1.Invalid, "current failure"),
				}
			}(),
			want: withDiag("invalid", "current failure"),
		},
		{
			name:   "other certificates' objects are ignored",
			reqs:   []cmapi.CertificateRequest{otherReq},
			orders: []acmev1.Order{otherOrd},
			want:   base(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCertStatus(cert, tt.reqs, tt.orders, tt.chals)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ToCertStatus() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestIssuerError(t *testing.T) {
	issuer := func(ns, name string, status cmmeta.ConditionStatus, reason, msg string) cmapi.Issuer {
		return cmapi.Issuer{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Status: cmapi.IssuerStatus{Conditions: []cmapi.IssuerCondition{{
				Type: cmapi.IssuerConditionReady, Status: status, Reason: reason, Message: msg,
			}}},
		}
	}
	clusterIssuer := func(name string, status cmmeta.ConditionStatus, reason, msg string) cmapi.ClusterIssuer {
		return cmapi.ClusterIssuer{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: cmapi.IssuerStatus{Conditions: []cmapi.IssuerCondition{{
				Type: cmapi.IssuerConditionReady, Status: status, Reason: reason, Message: msg,
			}}},
		}
	}
	const (
		ca    = "ca"
		other = "other"
		nope  = "secrets \"nope\" not found"
	)

	tests := []struct {
		name           string
		ref            cmmeta.IssuerReference // an empty Name is filled in with "ca"
		issuers        []cmapi.Issuer
		clusterIssuers []cmapi.ClusterIssuer
		want           string
	}{
		{
			name:    "issuer not ready: kind, name, reason and message",
			ref:     cmmeta.IssuerReference{Kind: "Issuer"},
			issuers: []cmapi.Issuer{issuer(testNamespace, ca, cmmeta.ConditionFalse, "ErrGetKeyPair", nope)},
			want:    "Issuer ca: ErrGetKeyPair: " + nope,
		},
		{
			name:    "kind defaults to Issuer",
			issuers: []cmapi.Issuer{issuer(testNamespace, ca, cmmeta.ConditionFalse, "ErrGetKeyPair", nope)},
			want:    "Issuer ca: ErrGetKeyPair: " + nope,
		},
		{
			name:           "cluster issuer not ready",
			ref:            cmmeta.IssuerReference{Kind: "ClusterIssuer"},
			clusterIssuers: []cmapi.ClusterIssuer{clusterIssuer(ca, cmmeta.ConditionFalse, "ErrRegisterACMEAccount", "bad email")},
			want:           "ClusterIssuer ca: ErrRegisterACMEAccount: bad email",
		},
		{
			name:    "issuer ready",
			issuers: []cmapi.Issuer{issuer(testNamespace, ca, cmmeta.ConditionTrue, "KeyPairVerified", "ok")},
		},
		{
			name:    "issuer in another namespace is not this certificate's",
			issuers: []cmapi.Issuer{issuer("elsewhere", ca, cmmeta.ConditionFalse, "ErrGetKeyPair", nope)},
		},
		{
			name:    "issuer with another name is not this certificate's",
			issuers: []cmapi.Issuer{issuer(testNamespace, other, cmmeta.ConditionFalse, "ErrGetKeyPair", nope)},
		},
		{
			name:           "an Issuer of the same name is not the ClusterIssuer",
			ref:            cmmeta.IssuerReference{Kind: "ClusterIssuer"},
			issuers:        []cmapi.Issuer{issuer(testNamespace, ca, cmmeta.ConditionFalse, "ErrGetKeyPair", nope)},
			clusterIssuers: nil,
		},
		{
			name: "no Ready condition yet",
			issuers: []cmapi.Issuer{{
				ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: ca},
			}},
		},
		{
			name:    "external issuer group is not read",
			ref:     cmmeta.IssuerReference{Group: "awspca.cert-manager.io", Kind: "AWSPCAIssuer"},
			issuers: []cmapi.Issuer{issuer(testNamespace, ca, cmmeta.ConditionFalse, "ErrGetKeyPair", nope)},
		},
		{
			name: "no issuers read at all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert := certificate()
			cert.Spec.IssuerRef = tt.ref
			if cert.Spec.IssuerRef.Name == "" {
				cert.Spec.IssuerRef.Name = ca
			}
			if got := IssuerError(cert, tt.issuers, tt.clusterIssuers); got != tt.want {
				t.Errorf("IssuerError() = %q, want %q", got, tt.want)
			}
		})
	}
}

// cert-manager leaves status fields nil until they are known, e.g. on a
// Certificate that has never been issued. That must map to zero values,
// not a panic, so report sees it as NeverIssued.
func TestToCertStatus_NilStatusFields(t *testing.T) {
	cert := &cmapi.Certificate{ObjectMeta: meta("new", "new-uid", t0, nil)}

	got := ToCertStatus(cert, nil, nil, nil)

	want := report.CertStatus{Namespace: testNamespace, Name: "new"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ToCertStatus() = %+v, want %+v", got, want)
	}
}
