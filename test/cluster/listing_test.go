//go:build cluster

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

// Package cluster holds validation tests that run against a real cluster
// with cert-manager installed, using the fixtures in testdata/. Run them with
// `make test-cluster`; the build tag keeps them out of `make test`.
package cluster

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/sakshitposting-irl/cyclops/internal/certmanager"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

const (
	fixtures  = "testdata/fixtures.yaml"
	nsA       = "cyclops-test-a"
	nsB       = "cyclops-test-b"
	nsMissing = "cyclops-test-missing" // deliberately never created
)

// kubeContext is the kubeconfig context to test against: $CLUSTER_CONTEXT,
// or kind-cyclops by default.
func kubeContext() string {
	if c := os.Getenv("CLUSTER_CONTEXT"); c != "" {
		return c
	}
	return "kind-cyclops"
}

func kubectl(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--context", kubeContext()}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// setup applies the fixtures, waits for the certificates that can be issued,
// registers cleanup, and returns a client for the cluster.
func setup(t *testing.T) client.Client {
	t.Helper()

	// The fixtures are deleted afterwards, namespaces included, so refuse
	// anything that isn't a throwaway kind cluster.
	if ctx := kubeContext(); !strings.HasPrefix(ctx, "kind-") {
		t.Fatalf("refusing to run against context %q: only kind-* contexts are allowed", ctx)
	}
	kubectl(t, "get", "crd", "certificates.cert-manager.io") // fails clearly if cert-manager isn't installed

	kubectl(t, "apply", "-f", fixtures)
	t.Cleanup(func() { kubectl(t, "delete", "-f", fixtures, "--ignore-not-found") })
	kubectl(t, "wait", "--for=condition=Ready", "-n", nsA,
		"certificate/healthy", "certificate/expiring", "--timeout=60s")

	cfg, err := config.GetConfigWithContext(kubeContext())
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, cmapi.AddToScheme, acmev1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fixtureFindings runs Evaluate as of at and returns "Kind ns/name" for
// findings in the fixture namespaces only, so other certificates in the
// cluster don't affect the result.
func fixtureFindings(snap certmanager.Snapshot, at time.Time) []string {
	var out []string
	for _, f := range report.Evaluate(snap.CertStatuses(), at) {
		if f.Cert.Namespace == nsA || f.Cert.Namespace == nsB {
			out = append(out, string(f.Kind)+" "+f.Cert.Namespace+"/"+f.Cert.Name)
		}
	}
	return out
}

func TestListing(t *testing.T) {
	c := setup(t)

	// Today only the broken cert needs attention: the others were just
	// issued, so cert-manager isn't due to renew them yet.
	wantNow := []string{"NeverIssued " + nsB + "/broken"}

	t.Run("explicit namespaces", func(t *testing.T) {
		snap, err := certmanager.List(context.Background(), c, []string{nsA, nsB, nsMissing})
		if err != nil {
			t.Fatal(err)
		}

		certs := make([]string, 0, len(snap.Certificates))
		for _, cert := range snap.Certificates {
			certs = append(certs, cert.Namespace+"/"+cert.Name)
		}
		slices.Sort(certs)
		if want := []string{nsA + "/expiring", nsA + "/healthy", nsB + "/broken"}; !reflect.DeepEqual(certs, want) {
			t.Errorf("Certificates = %v, want %v", certs, want)
		}
		if got := fixtureFindings(snap, time.Now()); !reflect.DeepEqual(got, wantNow) {
			t.Errorf("findings = %v, want %v", got, wantNow)
		}
	})

	t.Run("all namespaces", func(t *testing.T) {
		snap, err := certmanager.List(context.Background(), c, nil)
		if err != nil {
			t.Fatal(err)
		}

		if got := fixtureFindings(snap, time.Now()); !reflect.DeepEqual(got, wantNow) {
			t.Errorf("findings = %v, want %v", got, wantNow)
		}
	})

	// A real overdue renewal would take hours to produce, so evaluate the
	// same snapshot as of a later date instead. cert-manager renews at 2/3
	// of a certificate's lifetime by default, so 15 days from now is past
	// expiring's renewal time (~day 13 of 20) but before it expires, and
	// still well before healthy's (~day 60 of 90).
	t.Run("renewal overdue as of 15 days from now", func(t *testing.T) {
		snap, err := certmanager.List(context.Background(), c, []string{nsA, nsB})
		if err != nil {
			t.Fatal(err)
		}

		want := []string{
			"NeverIssued " + nsB + "/broken",
			"RenewalOverdue " + nsA + "/expiring",
		}
		if got := fixtureFindings(snap, time.Now().Add(15*24*time.Hour)); !reflect.DeepEqual(got, want) {
			t.Errorf("findings = %v, want %v", got, want)
		}
	})
}
