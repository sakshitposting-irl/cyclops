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

const threshold = 30 * 24 * time.Hour // 30 days, the documented default

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// cert builds a CertStatus expiring at now+in. Use neverIssued for a cert
// with no NotAfter.
func cert(ns, name string, in time.Duration) CertStatus {
	return CertStatus{Namespace: ns, Name: name, NotAfter: now.Add(in)}
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
			name:  "outside threshold is not reported",
			certs: []CertStatus{cert("default", "web", days(60))},
			want:  nil,
		},
		{
			name:  "inside threshold is expiring soon",
			certs: []CertStatus{cert("default", "web", days(10))},
			want:  []Finding{{Cert: cert("default", "web", days(10)), Kind: KindExpiringSoon}},
		},
		{
			// "within 30 days" is inclusive.
			name:  "exactly at threshold is expiring soon",
			certs: []CertStatus{cert("default", "web", threshold)},
			want:  []Finding{{Cert: cert("default", "web", threshold), Kind: KindExpiringSoon}},
		},
		{
			name:  "one nanosecond past threshold is not reported",
			certs: []CertStatus{cert("default", "web", threshold+time.Nanosecond)},
			want:  nil,
		},
		{
			// Failures alone never cause inclusion (ADR 0009); only dates do.
			name: "failing but outside threshold is not reported",
			certs: []CertStatus{{
				Namespace: "default", Name: "web", NotAfter: now.Add(days(60)),
				FailedIssuanceAttempts: 5, State: "errored", Reason: "connection refused",
			}},
			want: nil,
		},
		{
			name:  "in the past is expired",
			certs: []CertStatus{cert("default", "web", -days(1))},
			want:  []Finding{{Cert: cert("default", "web", -days(1)), Kind: KindExpired}},
		},
		{
			// Matches crypto/x509: a cert is still valid at the instant of
			// NotAfter and expired only once now is after it.
			name:  "exactly now is expiring soon, not expired",
			certs: []CertStatus{cert("default", "web", 0)},
			want:  []Finding{{Cert: cert("default", "web", 0), Kind: KindExpiringSoon}},
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
				cert("default", "later", days(20)),
				cert("default", "fine", days(90)),
				cert("default", "expired", -days(3)),
				neverIssued("default", "broken"),
				cert("default", "sooner", days(5)),
			},
			want: []Finding{
				{Cert: neverIssued("default", "broken"), Kind: KindNeverIssued},
				{Cert: cert("default", "expired", -days(3)), Kind: KindExpired},
				{Cert: cert("default", "sooner", days(5)), Kind: KindExpiringSoon},
				{Cert: cert("default", "later", days(20)), Kind: KindExpiringSoon},
			},
		},
		{
			// Same expiry: order must still be stable, or the email would
			// shuffle rows from one day to the next.
			name: "ties broken by namespace then name",
			certs: []CertStatus{
				cert("prod", "b", days(7)),
				cert("dev", "z", days(7)),
				cert("prod", "a", days(7)),
			},
			want: []Finding{
				{Cert: cert("dev", "z", days(7)), Kind: KindExpiringSoon},
				{Cert: cert("prod", "a", days(7)), Kind: KindExpiringSoon},
				{Cert: cert("prod", "b", days(7)), Kind: KindExpiringSoon},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.certs, now, threshold)

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
		cert("default", "later", days(20)),
		cert("default", "sooner", days(5)),
	}
	before := append([]CertStatus(nil), certs...) // an independent copy

	Evaluate(certs, now, threshold)

	if !reflect.DeepEqual(certs, before) {
		t.Errorf("input was modified:\n  got  %+v\n  want %+v", certs, before)
	}
}
