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

// Package render turns report findings into the email: the data every
// template receives (ADR 0013), the built-in default template, and the
// subject line.
package render

import (
	"math"
	"time"

	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// TemplateData is what every template, default or custom, is executed with.
// It is a public contract (ADR 0013): fields may be added, but renaming or
// removing one breaks users' templates.
type TemplateData struct {
	// ReportName is the CertReport's name.
	ReportName string
	// GeneratedAt is when the report ran, in UTC.
	GeneratedAt time.Time
	// Findings are the certificates that need attention, most urgent first.
	// May be empty.
	Findings []Row
}

// Row is one certificate in the report, flattened for templates.
type Row struct {
	// Kind is why the certificate is reported: "NeverIssued", "Expired" or
	// "RenewalOverdue".
	Kind      string
	Namespace string
	Name      string
	// NotAfter is when the certificate expires, in UTC. Zero if never issued.
	NotAfter time.Time
	// DaysLeft is whole days until NotAfter, rounded down, so negative once
	// expired. Only meaningful when NotAfter is set.
	DaysLeft int
	// RenewalTime is when cert-manager is due to renew it, in UTC. Zero if
	// none is scheduled.
	RenewalTime            time.Time
	FailedIssuanceAttempts int
	// State and Reason are cert-manager's own diagnostics, verbatim
	// (ADR 0004). Either may be empty.
	State  string
	Reason string
}

// NewTemplateData builds the template data for a report named reportName,
// generated at now, from findings in the order Evaluate returned them.
func NewTemplateData(reportName string, now time.Time, findings []report.Finding) TemplateData {
	var templateData TemplateData
	templateData.ReportName = reportName
	templateData.GeneratedAt = now.UTC()

	for _, f := range findings {
		row := Row{
			Kind:                   string(f.Kind),
			Namespace:              f.Cert.Namespace,
			Name:                   f.Cert.Name,
			NotAfter:               f.Cert.NotAfter.UTC(),
			DaysLeft:               daysLeft(f.Cert.NotAfter, now),
			RenewalTime:            f.Cert.RenewalTime.UTC(),
			FailedIssuanceAttempts: f.Cert.FailedIssuanceAttempts,
			State:                  f.Cert.State,
			Reason:                 f.Cert.Reason,
		}
		templateData.Findings = append(templateData.Findings, row)
	}

	return templateData
}

// daysLeft is the number of whole days from now until notAfter, rounded
// down: 0.9 days → 0, -0.5 days → -1 (ADR 0013). It is 0 if notAfter is zero
// (never issued), rather than the distance back to year 1.
func daysLeft(notAfter, now time.Time) int {
	if notAfter.IsZero() {
		return 0
	}
	return int(math.Floor(notAfter.Sub(now).Hours() / 24))
}
