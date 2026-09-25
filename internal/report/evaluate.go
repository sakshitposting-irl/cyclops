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
	"cmp"
	"slices"
	"time"
)

// Kind is why a certificate was included in the report.
type Kind string

const (
	// KindNeverIssued means the certificate has no NotAfter: cert-manager has
	// never successfully issued it.
	KindNeverIssued Kind = "NeverIssued"
	// KindExpired means NotAfter is in the past.
	KindExpired Kind = "Expired"
	// KindRenewalOverdue means cert-manager should have renewed the
	// certificate by now (RenewalTime is more than RenewalGrace in the past)
	// and hasn't (ADR 0012).
	KindRenewalOverdue Kind = "RenewalOverdue"
)

// RenewalGrace is how long past RenewalTime a certificate may be before it's
// reported. cert-manager starts renewing at RenewalTime, so this keeps
// renewals that are still in progress out of the report (ADR 0012).
const RenewalGrace = time.Hour

// Finding is one certificate that needs attention, and why.
type Finding struct {
	Cert CertStatus
	Kind Kind
}

// Evaluate returns the certificates that need attention as of now: never
// issued, expired, or overdue for renewal. Findings are ordered most urgent
// first (never issued, then soonest NotAfter; ties by namespace, then name).
// certs is not modified.
//
// Inclusion depends on dates only; failure diagnostics never add a
// certificate on their own (ADR 0009, ADR 0012).
func Evaluate(certs []CertStatus, now time.Time) []Finding {
	var findings []Finding

	for _, c := range certs {
		if c.NotAfter.IsZero() {
			findings = append(findings, Finding{Cert: c, Kind: KindNeverIssued})
		} else if now.After(c.NotAfter) {
			findings = append(findings, Finding{Cert: c, Kind: KindExpired})
		} else if isOverdue(c, now) {
			findings = append(findings, Finding{Cert: c, Kind: KindRenewalOverdue})
		}
	}

	// The zero time sorts before any real date and past dates before future
	// ones, so ordering by NotAfter alone already puts NeverIssued first,
	// then Expired, then RenewalOverdue. Sorting findings (our own slice)
	// leaves the caller's certs untouched.
	slices.SortFunc(findings, func(a, b Finding) int {
		return cmp.Or(
			a.Cert.NotAfter.Compare(b.Cert.NotAfter),
			cmp.Compare(a.Cert.Namespace, b.Cert.Namespace),
			cmp.Compare(a.Cert.Name, b.Cert.Name),
		)
	})

	return findings
}

// isOverdue reports whether c's renewal is more than RenewalGrace late.
func isOverdue(c CertStatus, now time.Time) bool {
	if c.RenewalTime.IsZero() {
		return false
	}
	if now.After(c.RenewalTime.Add(RenewalGrace)) {
		return true
	}
	return false
}
