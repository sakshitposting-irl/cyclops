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
		name        string
		namespaces  []string
		wantFrom    []string // namespaces every kind's items should come from
		wantMissing []string
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
			// A typo must be reported, or the report would look clean.
			name:        "missing namespace is reported",
			namespaces:  []string{"a", missingNS},
			wantFrom:    []string{"a"},
			wantMissing: []string{missingNS},
		},
		{
			// Sorted, so reordering spec.namespaces doesn't change the report.
			name:        "missing namespaces sorted",
			namespaces:  []string{"typo", "a", missingNS},
			wantFrom:    []string{"a"},
			wantMissing: []string{missingNS, "typo"},
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
			if !reflect.DeepEqual(snap.MissingNamespaces, tt.wantMissing) {
				t.Errorf("MissingNamespaces = %v, want %v", snap.MissingNamespaces, tt.wantMissing)
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
