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
	"reflect"
	"slices"
	"testing"

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeCluster returns a fake client holding namespaces "a" and "b", each with
// one of every kind report mode lists. The fake client is an in-memory API
// server: List and Get behave like the real thing, no cluster needed.
func fakeCluster(t *testing.T) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, cmapi.AddToScheme, acmev1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	namespaces := []string{"a", "b"}
	const objsPerNamespace = 5 // Namespace, Certificate, CertificateRequest, Order, Challenge
	objs := make([]client.Object, 0, len(namespaces)*objsPerNamespace)
	for _, ns := range namespaces {
		m := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
		objs = append(objs,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
			&cmapi.Certificate{ObjectMeta: m("cert")},
			&cmapi.CertificateRequest{ObjectMeta: m("cert-1")},
			&acmev1.Order{ObjectMeta: m("cert-1-order")},
			&acmev1.Challenge{ObjectMeta: m("cert-1-chal")},
		)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// namespacesOf returns the sorted namespaces of objs, one entry per object,
// so a duplicate listing shows up as a repeated namespace.
func namespacesOf[T any, PT interface {
	*T
	GetNamespace() string
}](objs []T) []string {
	out := make([]string, 0, len(objs))
	for i := range objs {
		out = append(out, PT(&objs[i]).GetNamespace())
	}
	slices.Sort(out)
	return out
}

// missingNS is a namespace fakeCluster doesn't have, standing in for a typo.
const missingNS = "prodd"

func TestList(t *testing.T) {
	tests := []struct {
		name       string
		namespaces []string
		wantFrom   []string // namespaces every kind's items should come from
	}{
		{
			name:       "empty means all namespaces",
			namespaces: nil,
			wantFrom:   []string{"a", "b"},
		},
		{
			name:       "explicit namespace only",
			namespaces: []string{"a"},
			wantFrom:   []string{"a"},
		},
		{
			// Reporting it is the controller's job (ADR 0011); List just
			// finds nothing there and carries on with the others.
			name:       "missing namespace contributes nothing",
			namespaces: []string{"a", missingNS},
			wantFrom:   []string{"a"},
		},
		{
			// Listing a namespace twice would put its certs in the email twice.
			name:       "duplicate namespaces listed once",
			namespaces: []string{"a", "a"},
			wantFrom:   []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, err := List(context.Background(), fakeCluster(t), tt.namespaces)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}

			for kind, got := range map[string][]string{
				"Certificates": namespacesOf(snap.Certificates),
				"Requests":     namespacesOf(snap.Requests),
				"Orders":       namespacesOf(snap.Orders),
				"Challenges":   namespacesOf(snap.Challenges),
			} {
				if !reflect.DeepEqual(got, tt.wantFrom) {
					t.Errorf("%s from namespaces %v, want %v", kind, got, tt.wantFrom)
				}
			}
		})
	}
}

func TestSnapshot_CertStatuses(t *testing.T) {
	snap, err := List(context.Background(), fakeCluster(t), nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	got := snap.CertStatuses()

	names := make([]string, 0, len(got))
	for _, s := range got {
		names = append(names, s.Namespace+"/"+s.Name)
	}
	slices.Sort(names)
	if want := []string{"a/cert", "b/cert"}; !reflect.DeepEqual(names, want) {
		t.Errorf("CertStatuses() = %v, want %v", names, want)
	}
}

// TestList_IssuerError checks the whole path for ADR 0021: List reads the
// namespace's Issuers and every ClusterIssuer, and CertStatuses turns a
// not-ready one into the certificate's IssuerError.
func TestList_IssuerError(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cmapi.AddToScheme, acmev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	notReady := cmapi.IssuerStatus{Conditions: []cmapi.IssuerCondition{{
		Type: cmapi.IssuerConditionReady, Status: cmmeta.ConditionFalse,
		Reason: "ErrGetKeyPair", Message: "no CA",
	}}}
	cert := func(ns, issuerKind, issuerName string) *cmapi.Certificate {
		return &cmapi.Certificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cert"},
			Spec:       cmapi.CertificateSpec{IssuerRef: cmmeta.IssuerReference{Kind: issuerKind, Name: issuerName}},
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		cert("a", "Issuer", "ca"),
		cert("b", "ClusterIssuer", "shared"),
		&cmapi.Issuer{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "ca"}, Status: notReady},
		&cmapi.ClusterIssuer{ObjectMeta: metav1.ObjectMeta{Name: "shared"}, Status: notReady},
	).Build()

	snap, err := List(context.Background(), c, []string{"a", "b"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(snap.Issuers) != 1 || len(snap.ClusterIssuers) != 1 {
		t.Fatalf("List() read %d Issuers and %d ClusterIssuers, want 1 and 1", len(snap.Issuers), len(snap.ClusterIssuers))
	}

	got := map[string]string{}
	for _, s := range snap.CertStatuses() {
		got[s.Namespace] = s.IssuerError
	}
	want := map[string]string{
		"a": "Issuer ca: ErrGetKeyPair: no CA",
		"b": "ClusterIssuer shared: ErrGetKeyPair: no CA",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IssuerError by namespace = %v, want %v", got, want)
	}
}

// TestAnnotatedIngresses covers the Ingress lookup (ADR 0020).
// TODO: build a fake client with networkingv1 added to the scheme and these
// Ingresses, then assert the returned map (sort each slice before comparing):
//   - two annotated Ingresses sharing one secretName -> one key, two names
//   - an Ingress with two tls entries -> two keys
//   - an Ingress with a different cr-name -> skipped
//   - an Ingress with no annotation -> skipped
//   - a tls entry with an empty secretName -> skipped
//   - no Ingresses at all -> empty map, nil error
func TestAnnotatedIngresses(t *testing.T) {
	t.Skip("TODO: scaffold")
}

// TestListForIngresses covers the annotation-mode listing (ADR 0020).
// TODO: fake client with Ingresses and Certificates, then assert which
// Certificates come back and that Snapshot.Ingresses is set:
//   - Certificate whose (namespace, spec.secretName) matches an annotated
//     Ingress -> kept
//   - Certificate in the same namespace that no annotated Ingress references
//     -> dropped
//   - annotated Ingress whose secret has no Certificate -> nothing returned
//   - no annotated Ingresses -> empty Snapshot (must NOT list every namespace)
//   - Certificates in an unrelated namespace are never read
func TestListForIngresses(t *testing.T) {
	t.Skip("TODO: scaffold")
}

// TestCertStatusesIngresses covers attaching Ingress names to CertStatus.
func TestCertStatusesIngresses(t *testing.T) {
	cert := func(ns, name, secret string) cmapi.Certificate {
		return cmapi.Certificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec:       cmapi.CertificateSpec{SecretName: secret},
		}
	}
	const prod, qa = "ingress-prod", "ingress-qa"
	shared := []string{prod, qa}
	unsorted := []string{qa, prod}
	snap := Snapshot{
		Certificates: []cmapi.Certificate{cert("a", "one", "tls-one"), cert("a", "two", "tls-two")},
		Ingresses:    map[SecretKey][]string{{"a", "tls-one"}: unsorted},
	}

	statuses := snap.CertStatuses()
	if got := statuses[0].Ingresses; !slices.Equal(got, shared) {
		t.Errorf("Ingresses = %v, want sorted %v", got, shared)
	}
	if statuses[1].Ingresses != nil {
		t.Errorf("Certificate with no Ingress got %v, want nil", statuses[1].Ingresses)
	}
	if !slices.Equal(unsorted, []string{qa, prod}) {
		t.Errorf("CertStatuses reordered the map's slice: %v", unsorted)
	}

	snap.Ingresses = nil // namespace mode
	for _, st := range snap.CertStatuses() {
		if st.Ingresses != nil {
			t.Errorf("namespace mode: Ingresses = %v, want nil", st.Ingresses)
		}
	}
}
