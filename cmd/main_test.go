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
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the error paths of run that don't need a cluster.
// The happy path (manager actually starting) is covered by envtest in
// internal/controller, so it's deliberately not duplicated here.

// reportImageFlag is the --report-image flag run insists on, set to a test image.
const reportImageFlag = "--report-image=cyclops:test"

// requiredFlags are the flags run insists on before it touches the cluster.
var requiredFlags = []string{flagNamespace, reportImageFlag}

func TestRun_RequiresNamespaceAndImage(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")
	tests := map[string]struct {
		args []string
		want string
	}{
		"no namespace":    {args: []string{reportImageFlag}, want: "--namespace"},
		"no report image": {args: []string{flagNamespace}, want: "--report-image"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := run(context.Background(), tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() error = %v, want it to mention %s", err, tt.want)
			}
		})
	}
}

func TestRun_NamespaceDefaultsToPodNamespace(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "from-env")
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	// With the namespace coming from the environment, run gets past flag validation
	// and fails later, on the missing kubeconfig.
	err := run(context.Background(), []string{reportImageFlag, "--webhook-port=-1", "--metrics-bind-address=0"})
	if err == nil || !strings.Contains(err.Error(), "loading kubeconfig") {
		t.Fatalf("run() error = %v, want it to get as far as loading kubeconfig", err)
	}
}

func TestRun_RejectsUnknownFlag(t *testing.T) {
	err := run(context.Background(), []string{"--no-such-flag"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
	if !strings.Contains(err.Error(), "parsing flags") {
		t.Fatalf("expected flag-parsing error, got: %v", err)
	}
}

func TestRun_FailsWithoutKubeconfig(t *testing.T) {
	// Point KUBECONFIG at a file that doesn't exist and make sure the
	// in-cluster path can't kick in either, so GetConfig has nothing to load.
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	err := run(context.Background(), append([]string{"--webhook-port=-1", "--metrics-bind-address=0"}, requiredFlags...))
	if err == nil {
		t.Fatal("expected error when no kubeconfig is available, got nil")
	}
	if !strings.Contains(err.Error(), "loading kubeconfig") {
		t.Fatalf("expected kubeconfig error, got: %v", err)
	}
}
