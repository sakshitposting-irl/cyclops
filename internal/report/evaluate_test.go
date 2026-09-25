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

package report

import (
	"reflect"
	"testing"
	"time"
)

// now is a fixed "current time" for every test. Evaluate takes now as a
// parameter precisely so tests never depend on the real clock.
var now = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// issued builds a CertStatus that expires at now+expiresIn and that
// cert-manager is due to renew at now+renewsIn (negative = in the past).
func issued(ns, name string, expiresIn, renewsIn time.Duration) CertStatus {
	return CertStatus{Namespace: ns, Name: name, NotAfter: now.Add(expiresIn), RenewalTime: now.Add(renewsIn)}
}

// overdue builds a CertStatus whose renewal is late by more than the grace
// period and that expires at now+expiresIn.
func overdue(ns, name string, expiresIn time.Duration) CertStatus {
	return issued(ns, name, expiresIn, -2*RenewalGrace)
}

func neverIssued(ns, name string) CertStatus {
	return CertStatus{Namespace: ns, Name: name} // NotAfter left at its zero value
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name  string
		certs []CertStatus
		want  []Finding
	}{
		{
			name:  "no certificates",
			certs: nil,
			want:  nil,
		},
		{
			// Healthy: cert-manager hasn't reached the renewal time yet.
			name:  "renewal time in the future is not reported",
			certs: []CertStatus{issued("default", "web", days(60), days(30))},
			want:  nil,
		},
		{
			// A renewal that started recently is probably still running.
			name:  "renewal time passed within the grace period is not reported",
			certs: []CertStatus{issued("default", "web", days(30), -30*time.Minute)},
			want:  nil,
		},
		{
			name:  "exactly at the grace boundary is not reported",
			certs: []CertStatus{issued("default", "web", days(30), -RenewalGrace)},
			want:  nil,
		},
		{
			name:  "one nanosecond past the grace period is overdue",
			certs: []CertStatus{issued("default", "web", days(30), -RenewalGrace-time.Nanosecond)},
			want: []Finding{{
				Cert: issued("default", "web", days(30), -RenewalGrace-time.Nanosecond),
				Kind: KindRenewalOverdue,
			}},
		},
		{
			// No threshold any more: a cert close to expiry but not yet due
			// for renewal (short renewBefore) is not reported (ADR 0012).
			name:  "close to expiry but renewal not due is not reported",
			certs: []CertStatus{issued("default", "web", days(2), days(1))},
			want:  nil,
		},
		{
			name:  "issued without a renewal time is not overdue",
			certs: []CertStatus{{Namespace: "default", Name: "web", NotAfter: now.Add(days(30))}},
			want:  nil,
		},
		{
			// Failures alone never cause inclusion (ADR 0009); only dates do.
			name: "failing but renewal not due is not reported",
			certs: []CertStatus{{
				Namespace: "default", Name: "web",
				NotAfter: now.Add(days(60)), RenewalTime: now.Add(days(30)),
				FailedIssuanceAttempts: 5, State: "errored", Reason: "connection refused",
			}},
			want: nil,
		},
		{
			// Expired wins over overdue: an expired cert is also past renewal.
			name:  "in the past is expired",
			certs: []CertStatus{overdue("default", "web", -days(1))},
			want:  []Finding{{Cert: overdue("default", "web", -days(1)), Kind: KindExpired}},
		},
		{
			// Matches crypto/x509: a cert is still valid at the instant of
			// NotAfter and expired only once now is after it.
			name:  "exactly now is overdue, not expired",
			certs: []CertStatus{overdue("default", "web", 0)},
			want:  []Finding{{Cert: overdue("default", "web", 0), Kind: KindRenewalOverdue}},
		},
		{
			// The safety-net case: a cert cert-manager never managed to issue
			// must be reported, not silently skipped (ADR 0003).
			name:  "never issued is reported",
			certs: []CertStatus{neverIssued("default", "web")},
			want:  []Finding{{Cert: neverIssued("default", "web"), Kind: KindNeverIssued}},
		},
		{
			// Never-issued certs all share the zero NotAfter, so their order
			// rests entirely on the namespace/name tie-break.
			name: "never issued ordered by namespace",
			certs: []CertStatus{
				neverIssued("prod", "web"),
				neverIssued("dev", "web"),
			},
			want: []Finding{
				{Cert: neverIssued("dev", "web"), Kind: KindNeverIssued},
				{Cert: neverIssued("prod", "web"), Kind: KindNeverIssued},
			},
		},
		{
			name: "most urgent first",
			certs: []CertStatus{
				overdue("default", "later", days(20)),
				issued("default", "fine", days(90), days(60)),
				overdue("default", "expired", -days(3)),
				neverIssued("default", "broken"),
				overdue("default", "sooner", days(5)),
			},
			want: []Finding{
				{Cert: neverIssued("default", "broken"), Kind: KindNeverIssued},
				{Cert: overdue("default", "expired", -days(3)), Kind: KindExpired},
				{Cert: overdue("default", "sooner", days(5)), Kind: KindRenewalOverdue},
				{Cert: overdue("default", "later", days(20)), Kind: KindRenewalOverdue},
			},
		},
		{
			// Same expiry: order must still be stable, or the email would
			// shuffle rows from one day to the next.
			name: "ties broken by namespace then name",
			certs: []CertStatus{
				overdue("prod", "b", days(7)),
				overdue("dev", "z", days(7)),
				overdue("prod", "a", days(7)),
			},
			want: []Finding{
				{Cert: overdue("dev", "z", days(7)), Kind: KindRenewalOverdue},
				{Cert: overdue("prod", "a", days(7)), Kind: KindRenewalOverdue},
				{Cert: overdue("prod", "b", days(7)), Kind: KindRenewalOverdue},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.certs, now)

			// Treat nil and empty as the same "nothing to report".
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Evaluate() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

// Evaluate must not reorder the caller's slice while sorting its result.
func TestEvaluate_DoesNotModifyInput(t *testing.T) {
	certs := []CertStatus{
		overdue("default", "later", days(20)),
		overdue("default", "sooner", days(5)),
	}
	before := append([]CertStatus(nil), certs...) // an independent copy

	Evaluate(certs, now)

	if !reflect.DeepEqual(certs, before) {
		t.Errorf("input was modified:\n  got  %+v\n  want %+v", certs, before)
	}
}
