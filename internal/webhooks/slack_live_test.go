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

package webhooks

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sakshitposting-irl/cyclops/internal/render"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// TestLiveSlack posts real reports to the webhook in CYCLOPS_TEST_SLACK_URL, with the real waits. It is
// skipped when the variable is unset. Point it at a Slack incoming webhook, or at any local receiver.
//
//	CYCLOPS_TEST_SLACK_URL=https://hooks.slack.com/services/... go test ./internal/webhooks/ -run TestLiveSlack -v
func TestLiveSlack(t *testing.T) {
	url := os.Getenv("CYCLOPS_TEST_SLACK_URL")
	if url == "" {
		t.Skip("CYCLOPS_TEST_SLACK_URL is not set")
	}
	s, err := NewSlack("live", url)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// One of each kind, with a multi-line reason and text Slack must escape.
	mixed := render.TemplateData{ReportName: "live-mixed", GeneratedAt: now, Findings: []render.Row{
		{Kind: string(report.KindExpired), Namespace: "shop", Name: "api-tls", DaysLeft: -3},
		{Kind: string(report.KindRenewalOverdue), Namespace: "auth", Name: "login-tls", Reason: "Failed to finalize order:\n  rate limited"},
		{Kind: string(report.KindNeverIssued), Namespace: "blog", Name: "www-tls", IssuerError: "Issuer broken-ca: secret <ca-key> & <!channel> not found"},
	}}

	// Enough findings for three parts, to see the gap between them.
	split := render.TemplateData{ReportName: "live-split", GeneratedAt: now}
	for i := range findingsPerMessage*2 + 4 {
		split.Findings = append(split.Findings, render.Row{Kind: string(report.KindExpired), Namespace: "ns", Name: fmt.Sprintf("cert-%03d", i+1), DaysLeft: -1})
	}

	for _, data := range []render.TemplateData{
		{ReportName: "live-healthy", GeneratedAt: now},
		{ReportName: "live-welcome", GeneratedAt: now, Welcome: true, Schedule: "0 9 * * *", Namespaces: []string{"shop", "auth"}},
		mixed,
		split,
	} {
		start := time.Now()
		if err := s.Send(context.Background(), data); err != nil {
			t.Errorf("%s: %v", data.ReportName, err)
		}
		t.Logf("%s: %d message(s) in %v", data.ReportName, len(buildMessages(data)), time.Since(start).Round(time.Millisecond))
	}
}
