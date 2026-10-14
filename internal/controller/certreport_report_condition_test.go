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
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

const reasonLastRunFailed = "LastRunFailed"

func TestReportCondition(t *testing.T) {
	t0 := metav1.NewTime(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC))
	t1 := metav1.NewTime(t0.Add(24 * time.Hour))
	failedBefore := &metav1.Condition{
		Type: cyclopsv1alpha1.ConditionReportSucceeding, Status: metav1.ConditionFalse,
		Reason: reasonLastRunFailed, Message: "the last scheduled run at old failed",
	}
	active := []corev1.ObjectReference{{Name: "cyclops-x-123"}}

	tests := []struct {
		name       string
		suspend    *bool
		status     batchv1.CronJobStatus
		prev       *metav1.Condition
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name:       "suspended",
			suspend:    new(true),
			status:     batchv1.CronJobStatus{LastScheduleTime: &t0},
			wantStatus: metav1.ConditionUnknown, wantReason: "Suspended",
		},
		{
			name:       "never scheduled",
			wantStatus: metav1.ConditionUnknown, wantReason: "NoRunsYet",
		},
		{
			name:       "first run in flight and nothing stored",
			status:     batchv1.CronJobStatus{LastScheduleTime: &t0, Active: active},
			wantStatus: metav1.ConditionUnknown, wantReason: "RunInProgress",
		},
		{
			name:       "run in flight keeps a stored False",
			status:     batchv1.CronJobStatus{LastScheduleTime: &t1, LastSuccessfulTime: &t0, Active: active},
			prev:       failedBefore,
			wantStatus: metav1.ConditionFalse, wantReason: reasonLastRunFailed,
		},
		{
			name:       "scheduled but never succeeded",
			status:     batchv1.CronJobStatus{LastScheduleTime: &t0},
			wantStatus: metav1.ConditionFalse, wantReason: reasonLastRunFailed,
		},
		{
			name:       "last success older than last schedule",
			status:     batchv1.CronJobStatus{LastScheduleTime: &t1, LastSuccessfulTime: &t0},
			wantStatus: metav1.ConditionFalse, wantReason: reasonLastRunFailed,
		},
		{
			name:       "last success is the latest run",
			status:     batchv1.CronJobStatus{LastScheduleTime: &t0, LastSuccessfulTime: &t0},
			wantStatus: metav1.ConditionTrue, wantReason: "LastRunSucceeded",
		},
		{
			name:       "recovers after a failure",
			status:     batchv1.CronJobStatus{LastScheduleTime: &t1, LastSuccessfulTime: &t1},
			prev:       failedBefore,
			wantStatus: metav1.ConditionTrue, wantReason: "LastRunSucceeded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cj := &batchv1.CronJob{
				Spec:   batchv1.CronJobSpec{Suspend: tc.suspend},
				Status: tc.status,
			}
			got := reportCondition(cj, tc.prev)
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("got %s/%s, want %s/%s", got.Status, got.Reason, tc.wantStatus, tc.wantReason)
			}
			if got.Type != cyclopsv1alpha1.ConditionReportSucceeding {
				t.Fatalf("type = %q", got.Type)
			}
			if again := reportCondition(cj, tc.prev); again != got {
				t.Fatalf("message is not stable between calls: %q vs %q", got.Message, again.Message)
			}
		})
	}
}

func TestReportConditionKeepsStoredConditionWhileRunning(t *testing.T) {
	t0 := metav1.NewTime(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC))
	prev := &metav1.Condition{Type: cyclopsv1alpha1.ConditionReportSucceeding, Status: metav1.ConditionFalse, Reason: reasonLastRunFailed, Message: "keep me"}
	cj := &batchv1.CronJob{Status: batchv1.CronJobStatus{
		LastScheduleTime: &t0, Active: []corev1.ObjectReference{{Name: "j"}},
	}}
	if got := reportCondition(cj, prev); got != *prev {
		t.Fatalf("stored condition changed while a run was active: %+v", got)
	}
}
