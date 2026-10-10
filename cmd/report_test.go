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

package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
	"github.com/sakshitposting-irl/cyclops/internal/email"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// Values shared by the report tests.
const (
	testCyclopsNS  = "cyclops-system"
	testReportName = "prod-certs"
)

func TestGenerateReport_MissingCertReportIsAnError(t *testing.T) {
	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()

	err = generateReport(context.Background(), c, nil,
		reportOptions{CertReportName: "gone", Namespace: testCyclopsNS}, time.Now())
	if err == nil || !strings.Contains(err.Error(), `CertReport "gone" not found`) {
		t.Fatalf("generateReport() error = %v, want a not-found error naming the CertReport", err)
	}
}

func certificate(namespace, name string, notAfter *time.Time) *cmapi.Certificate {
	cert := &cmapi.Certificate{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	if notAfter != nil {
		cert.Status.NotAfter = &metav1.Time{Time: *notAfter}
	}
	return cert
}

func TestFindCertificates(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	expired := now.Add(-24 * time.Hour)
	healthy := now.Add(30 * 24 * time.Hour)

	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		certificate("a", "old", &expired),
		certificate("a", "fine", &healthy),
		certificate("b", "never", nil),
		certificate("c", "old-too", &expired),
	).Build()

	tests := map[string]struct {
		namespaces []string
		want       []string // "kind namespace/name", most urgent first
	}{
		"all namespaces": {
			want: []string{"NeverIssued b/never", "Expired a/old", "Expired c/old-too"},
		},
		"one namespace": {
			namespaces: []string{"a"},
			want:       []string{"Expired a/old"},
		},
		"a namespace that does not exist contributes nothing": {
			namespaces: []string{"missing"},
			want:       nil,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			findings, err := findCertificates(context.Background(), c, tt.namespaces, now)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, f := range findings {
				got = append(got, kindName(f)+" "+f.Cert.Namespace+"/"+f.Cert.Name)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("findings = %v, want %v", got, tt.want)
			}
		})
	}
}

func kindName(f report.Finding) string { return string(f.Kind) }

// reportFixture is a cluster with one healthy and (optionally) one expired certificate, a
// CertReport named testReportName, and a sender factory that records whether it was called.
func reportFixture(t *testing.T, withExpired bool) (c client.Reader, newSender senderFactory, called *bool) {
	t.Helper()
	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	healthy := now.Add(30 * 24 * time.Hour)
	expired := now.Add(-24 * time.Hour)

	objs := []client.Object{
		&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: testReportName}},
		certificate("a", "fine", &healthy),
	}
	if withExpired {
		objs = append(objs, certificate("a", "old", &expired))
	}
	called = new(bool)
	newSender = func(context.Context) (email.Sender, error) {
		*called = true
		return nil, errors.New("sender factory called")
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build(), newSender, called
}

func TestGenerateReport_NothingToReport(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	opts := reportOptions{CertReportName: testReportName, Namespace: testCyclopsNS}

	t.Run("a healthy day exits cleanly without touching the mail settings", func(t *testing.T) {
		c, newSender, called := reportFixture(t, false)
		if err := generateReport(context.Background(), c, newSender, opts, now); err != nil {
			t.Fatalf("generateReport() error = %v, want nil on a healthy day", err)
		}
		if *called {
			t.Fatal("the sender factory was called on a healthy day (ADR 0015)")
		}
	})

	t.Run("findings carry on past the early exit", func(t *testing.T) {
		c, newSender, _ := reportFixture(t, true)
		err := generateReport(context.Background(), c, newSender, opts, now)
		if !errors.Is(err, errReportNotImplemented) {
			t.Fatalf("generateReport() error = %v, want it to reach the not-yet-written rest", err)
		}
	})

	t.Run("the welcome run carries on even with no findings", func(t *testing.T) {
		c, newSender, _ := reportFixture(t, false)
		welcome := opts
		welcome.Welcome = true
		err := generateReport(context.Background(), c, newSender, welcome, now)
		if !errors.Is(err, errReportNotImplemented) {
			t.Fatalf("generateReport() error = %v, want it to reach the not-yet-written rest", err)
		}
	})
}
