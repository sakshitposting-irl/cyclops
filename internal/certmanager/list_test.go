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
	networkingv1 "k8s.io/api/networking/v1"
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

const (
	testAnnotation = "example.com/cr-name" // the key is a parameter, so any key will do
	testCR         = "prod-certs"
)

// ingressCluster returns a fake client holding objs, with Ingresses in the scheme.
func ingressCluster(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, cmapi.AddToScheme, acmev1.AddToScheme, networkingv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// ingress builds an Ingress with the given tls secret names. An empty cr leaves it unannotated.
func ingress(ns, name, cr string, secrets ...string) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if cr != "" {
		ing.Annotations = map[string]string{testAnnotation: cr}
	}
	for _, sec := range secrets {
		ing.Spec.TLS = append(ing.Spec.TLS, networkingv1.IngressTLS{SecretName: sec})
	}
	return ing
}

// certWithSecret builds a Certificate that writes its certificate to the given Secret.
func certWithSecret(ns, name, secret string) *cmapi.Certificate {
	return &cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       cmapi.CertificateSpec{SecretName: secret},
	}
}

// TestAnnotatedIngresses covers the Ingress lookup (ADR 0020).
func TestAnnotatedIngresses(t *testing.T) {
	t.Run("only Ingresses annotated for this CertReport count", func(t *testing.T) {
		c := ingressCluster(t,
			// Two Ingresses share a secret, and one has two tls entries.
			ingress("a", "prod", testCR, "shared-tls", "second-tls"),
			ingress("a", "qa", testCR, "shared-tls"),
			ingress("a", "other-report", "other-certs", "other-tls"), // another CertReport's
			ingress("a", "plain", "", "plain-tls"),                   // not annotated
			ingress("b", "no-secret", testCR, ""),                    // tls entry without a secretName
		)

		got, err := AnnotatedIngresses(context.Background(), c, testCR, testAnnotation)
		if err != nil {
			t.Fatalf("AnnotatedIngresses() error = %v", err)
		}
		for _, names := range got {
			slices.Sort(names)
		}
		want := map[SecretKey][]string{
			{"a", "shared-tls"}: {"prod", "qa"},
			{"a", "second-tls"}: {"prod"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("AnnotatedIngresses() = %v, want %v", got, want)
		}
	})

	t.Run("no Ingresses: empty map", func(t *testing.T) {
		got, err := AnnotatedIngresses(context.Background(), ingressCluster(t), testCR, testAnnotation)
		if err != nil {
			t.Fatalf("AnnotatedIngresses() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("AnnotatedIngresses() = %v, want empty", got)
		}
	})
}

// TestListForIngresses covers the annotation-mode listing (ADR 0020).
func TestListForIngresses(t *testing.T) {
	ctx := context.Background()

	t.Run("keeps the Certificate behind an annotated Ingress and nothing else", func(t *testing.T) {
		c := ingressCluster(t,
			ingress("a", "shop", testCR, "shop-tls"),
			certWithSecret("a", "kept", "shop-tls"),
			certWithSecret("a", "dropped", "internal-tls"), // same namespace, no annotated Ingress
			// Same secret name, but no annotated Ingress in this namespace: never read.
			certWithSecret("z", "unrelated", "shop-tls"),
			&cmapi.CertificateRequest{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "kept-1"}},
			&cmapi.CertificateRequest{ObjectMeta: metav1.ObjectMeta{Namespace: "z", Name: "unrelated-1"}},
		)

		snap, err := ListForIngresses(ctx, c, testCR, testAnnotation)
		if err != nil {
			t.Fatalf("ListForIngresses() error = %v", err)
		}

		if got := namespacesOf(snap.Certificates); !reflect.DeepEqual(got, []string{"a"}) || snap.Certificates[0].Name != "kept" {
			t.Errorf("Certificates = %v, want only a/kept", snap.Certificates)
		}
		if got := namespacesOf(snap.Requests); !reflect.DeepEqual(got, []string{"a"}) {
			t.Errorf("Requests read from %v, want only namespace a", got)
		}
		if want := map[SecretKey][]string{{"a", "shop-tls"}: {"shop"}}; !reflect.DeepEqual(snap.Ingresses, want) {
			t.Errorf("Ingresses = %v, want %v", snap.Ingresses, want)
		}
	})

	t.Run("annotated Ingress whose secret has no Certificate: nothing returned", func(t *testing.T) {
		c := ingressCluster(t,
			ingress("a", "shop", testCR, "shop-tls"),
			certWithSecret("a", "other", "other-tls"),
		)

		snap, err := ListForIngresses(ctx, c, testCR, testAnnotation)
		if err != nil {
			t.Fatalf("ListForIngresses() error = %v", err)
		}
		if len(snap.Certificates) != 0 {
			t.Errorf("Certificates = %v, want none", snap.Certificates)
		}
	})

	t.Run("no annotated Ingresses: empty Snapshot, not every namespace", func(t *testing.T) {
		c := ingressCluster(t,
			ingress("a", "shop", "other-certs", "shop-tls"), // annotated for another CertReport
			certWithSecret("a", "kept-by-nobody", "shop-tls"),
			&cmapi.CertificateRequest{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "r"}},
		)

		snap, err := ListForIngresses(ctx, c, testCR, testAnnotation)
		if err != nil {
			t.Fatalf("ListForIngresses() error = %v", err)
		}
		if len(snap.Certificates) != 0 || len(snap.Requests) != 0 || len(snap.Ingresses) != 0 {
			t.Errorf("Snapshot = %+v, want empty (an empty scope must not list every namespace)", snap)
		}
	})
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
