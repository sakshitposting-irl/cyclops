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

	err := run(context.Background(), []string{"--webhook-port=-1", "--metrics-bind-address=0"})
	if err == nil {
		t.Fatal("expected error when no kubeconfig is available, got nil")
	}
	if !strings.Contains(err.Error(), "loading kubeconfig") {
		t.Fatalf("expected kubeconfig error, got: %v", err)
	}
}
