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

package controller

import (
	"regexp"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

func TestWelcomeJobName(t *testing.T) {
	const hash = "0123abcd"

	tests := []struct {
		name   string
		crName string
		want   string
	}{
		{"a short name is used whole", "prod", "cyclops-welcome-prod-0123abcd"},
		{"a name that exactly fits is kept", strings.Repeat("a", 38), "cyclops-welcome-" + strings.Repeat("a", 38) + "-0123abcd"},
		{"a longer name is cut to fit", strings.Repeat("a", 39), "cyclops-welcome-" + strings.Repeat("a", 38) + "-0123abcd"},
		{"a cut that ends in a hyphen is trimmed", strings.Repeat("a", 37) + "-bbbb", "cyclops-welcome-" + strings.Repeat("a", 37) + "-0123abcd"},
		{"a cut that ends in a dot is trimmed", strings.Repeat("a", 37) + ".bbbb", "cyclops-welcome-" + strings.Repeat("a", 37) + "-0123abcd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := welcomeJobName(tt.crName, hash)
			if got != tt.want {
				t.Errorf("welcomeJobName(%q) = %q, want %q", tt.crName, got, tt.want)
			}
			if len(got) > maxJobNameLen {
				t.Errorf("name is %d characters, want at most %d", len(got), maxJobNameLen)
			}
		})
	}

	t.Run("a 253-character CertReport name still fits, and keeps the whole hash", func(t *testing.T) {
		got := welcomeJobName(strings.Repeat("a", 253), hash)
		if len(got) > maxJobNameLen || !strings.HasSuffix(got, "-"+hash) {
			t.Errorf("welcomeJobName() = %q (%d characters)", got, len(got))
		}
	})
}

func TestWelcomeHash(t *testing.T) {
	report := func(name string, generation int64) *cyclopsv1alpha1.CertReport {
		return &cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: name, Generation: generation}}
	}
	base := welcomeHash(report("prod", 1), "100")

	t.Run("it is 8 lowercase hex characters, so welcomeJobName has room for the name", func(t *testing.T) {
		if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(base) {
			t.Errorf("welcomeHash() = %q, want 8 hex characters", base)
		}
	})

	t.Run("the same inputs always give the same hash", func(t *testing.T) {
		if got := welcomeHash(report("prod", 1), "100"); got != base {
			t.Errorf("welcomeHash() = %q, then %q", base, got)
		}
	})

	// Each input must change the hash: it is what makes a retry possible after a fix, and what
	// keeps two CertReports from sharing a Job name.
	tests := []struct {
		name string
		cr   *cyclopsv1alpha1.CertReport
		rv   string
	}{
		{"the CertReport was edited (generation)", report("prod", 2), "100"},
		{"the credentials Secret changed (resourceVersion)", report("prod", 1), "101"},
		{"a different CertReport (name)", report("dev", 1), "100"},
		{"no credentials Secret", report("prod", 1), ""},
	}
	for _, tt := range tests {
		t.Run("changes when "+tt.name, func(t *testing.T) {
			if got := welcomeHash(tt.cr, tt.rv); got == base {
				t.Errorf("welcomeHash() = %q, same as the base hash", got)
			}
		})
	}

	// Without a separator, generation 1 + version "2" and generation 12 + no version would both
	// be "12".
	t.Run("generation and version are not run together", func(t *testing.T) {
		if welcomeHash(report("prod", 1), "2") == welcomeHash(report("prod", 12), "") {
			t.Error("(generation 1, version 2) and (generation 12, version none) hash the same")
		}
	})

	t.Run("two long names with the same start still get different Job names", func(t *testing.T) {
		// Both are cut to the same 38 characters by welcomeJobName.
		start := strings.Repeat("a", 38)
		jobA := welcomeJobName(start+"-east", welcomeHash(report(start+"-east", 1), "100"))
		jobB := welcomeJobName(start+"-west", welcomeHash(report(start+"-west", 1), "100"))
		if jobA == jobB {
			t.Errorf("both CertReports would get the Job %q", jobA)
		}
		if len(jobA) > maxJobNameLen || len(jobB) > maxJobNameLen {
			t.Errorf("names too long: %q, %q", jobA, jobB)
		}
	})
}

func TestWelcomeCondition(t *testing.T) {
	const (
		jobName = "cyclops-welcome-prod-0123abcd"
		running = "Running"
	)

	job := func(conds ...batchv1.JobCondition) *batchv1.Job {
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: jobName},
			Status:     batchv1.JobStatus{Conditions: conds},
		}
	}
	cond := func(t batchv1.JobConditionType, s corev1.ConditionStatus) batchv1.JobCondition {
		return batchv1.JobCondition{Type: t, Status: s}
	}

	tests := []struct {
		name       string
		job        *batchv1.Job
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"no conditions yet means it is running", job(), metav1.ConditionUnknown, running},
		{"Complete is a success", job(cond(batchv1.JobComplete, corev1.ConditionTrue)), metav1.ConditionTrue, "Sent"},
		{"Failed is a failure", job(cond(batchv1.JobFailed, corev1.ConditionTrue)), metav1.ConditionFalse, "JobFailed"},
		{"a Failed condition that is not True does not count", job(cond(batchv1.JobFailed, corev1.ConditionFalse)), metav1.ConditionUnknown, running},
		{"a Complete condition that is not True does not count", job(cond(batchv1.JobComplete, corev1.ConditionFalse)), metav1.ConditionUnknown, running},
		{"other condition types are ignored", job(cond(batchv1.JobSuspended, corev1.ConditionTrue)), metav1.ConditionUnknown, running},
		{"a terminal condition is found among others", job(cond(batchv1.JobSuspended, corev1.ConditionFalse), cond(batchv1.JobFailed, corev1.ConditionTrue)), metav1.ConditionFalse, "JobFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := welcomeCondition(tt.job)
			if got.Type != cyclopsv1alpha1.ConditionWelcomeSent {
				t.Errorf("Type = %q, want %q", got.Type, cyclopsv1alpha1.ConditionWelcomeSent)
			}
			if got.Status != tt.wantStatus || got.Reason != tt.wantReason {
				t.Errorf("got %s/%s, want %s/%s", got.Status, got.Reason, tt.wantStatus, tt.wantReason)
			}
			if !strings.Contains(got.Message, jobName) {
				t.Errorf("Message = %q, want it to name the Job %q", got.Message, jobName)
			}
		})
	}

	// The message is compared by warnOnTransition and updateStatus: if it changed between calls,
	// every reconcile would write status.
	t.Run("the message is the same on every call", func(t *testing.T) {
		j := job(cond(batchv1.JobFailed, corev1.ConditionTrue))
		if a, b := welcomeCondition(j), welcomeCondition(j); a.Message != b.Message {
			t.Errorf("Message changed: %q then %q", a.Message, b.Message)
		}
	})
}
