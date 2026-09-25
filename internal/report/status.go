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

// Package report holds cyclops's reporting logic: deciding which
// certificates need attention, independent of Kubernetes and cert-manager.
package report

import (
	"time"
)

// CertStatus is cyclops's own view of a single certificate. It is
// deliberately not cert-manager's Certificate type: report mode converts
// cert-manager objects into this at the edge, so the logic in this package
// stays plain Go and can be tested without a cluster.
type CertStatus struct {
	// Namespace is the namespace the Certificate lives in.
	Namespace string
	// Name is the Certificate's name.
	Name string

	// NotAfter is when the certificate expires. The zero value means the
	// certificate has never been issued; check with NotAfter.IsZero().
	NotAfter time.Time
	// RenewalTime is when cert-manager plans to renew the certificate. The
	// zero value means cert-manager has not scheduled a renewal.
	RenewalTime time.Time

	// The fields below are failure diagnostics (ADR 0004). They are relayed
	// to the reader as-is and never interpreted or categorized by cyclops.

	// FailedIssuanceAttempts is how many issuance attempts in a row have
	// failed. Zero means none have failed since the last success.
	FailedIssuanceAttempts int
	// State is the ACME state of the most specific failing Order or
	// Challenge (e.g. "pending", "invalid", "errored"). Empty if there is
	// none.
	State string
	// Reason is cert-manager's free-text reason for State, copied
	// verbatim. Its wording is not stable across cert-manager versions, so
	// it is for humans to read, not for code to match on.
	Reason string
}
