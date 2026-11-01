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
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sakshitposting-irl/cyclops/internal/render"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// Test values that appear in several cases.
const (
	testProd = "prod"
	testDev  = "dev"
	testAPI  = "api-tls"
)

var testNow = time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)

func testData(rows ...render.Row) render.TemplateData {
	return render.TemplateData{ReportName: "daily", GeneratedAt: testNow, Findings: rows}
}

func TestFindingText(t *testing.T) {
	tests := []struct {
		name string
		row  render.Row
		want string
	}{
		{
			"expired with a reason",
			render.Row{Kind: string(report.KindExpired), Namespace: testProd, Name: testAPI, DaysLeft: -2, Reason: "acme: authorization failed"},
			"🔴 prod/api-tls — expired 2 days ago\n     ↳ acme: authorization failed",
		},
		{
			"expired one day ago is singular",
			render.Row{Kind: string(report.KindExpired), Namespace: testProd, Name: testAPI, DaysLeft: -1},
			"🔴 prod/api-tls — expired 1 day ago",
		},
		{
			"renewal overdue without detail",
			render.Row{Kind: string(report.KindRenewalOverdue), Namespace: testProd, Name: "web-tls", DaysLeft: 12},
			"🟠 prod/web-tls — renewal overdue",
		},
		{
			"never issued with an issuer error",
			render.Row{Kind: string(report.KindNeverIssued), Namespace: testDev, Name: "new-tls", IssuerError: "Issuer broken-ca: ErrGetKeyPair"},
			"⚪ dev/new-tls — never issued\n     ↳ Issuer broken-ca: ErrGetKeyPair",
		},
		{
			"reason and issuer error each get a line",
			render.Row{Kind: string(report.KindNeverIssued), Namespace: testDev, Name: "x", Reason: "Pending", IssuerError: "not ready"},
			"⚪ dev/x — never issued\n     ↳ Pending\n     ↳ not ready",
		},
		{
			"newlines in a reason become spaces",
			render.Row{Kind: string(report.KindNeverIssued), Namespace: testDev, Name: "x", Reason: "line one\n\nline  two"},
			"⚪ dev/x — never issued\n     ↳ line one line two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findingText(tt.row); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFindingTextEscapesAndCapsUntrustedText(t *testing.T) {
	got := findingText(render.Row{
		Kind: string(report.KindExpired), Namespace: testProd, Name: "x", DaysLeft: -1,
		Reason: "<!channel> a & b",
	})
	if strings.Contains(got, "<!channel>") {
		t.Errorf("an unescaped <!channel> would ping the channel: %s", got)
	}
	if !strings.Contains(got, "&lt;!channel&gt; a &amp; b") {
		t.Errorf("want escaped text, got: %s", got)
	}

	long := findingText(render.Row{Kind: string(report.KindNeverIssued), Namespace: "a", Name: "b", Reason: strings.Repeat("é", 1000)})
	_, detail, _ := strings.Cut(long, "↳ ")
	if n := utf8.RuneCountInString(detail); n != maxDetail || !strings.HasSuffix(detail, "…") {
		t.Errorf("want a reason cut to %d characters ending in an ellipsis, got %d: %q", maxDetail, n, detail)
	}
}

func TestBuildMessagesOneMessage(t *testing.T) {
	msgs := buildMessages(testData(
		render.Row{Kind: string(report.KindExpired), Namespace: testProd, Name: testAPI, DaysLeft: -2},
		render.Row{Kind: string(report.KindNeverIssued), Namespace: testDev, Name: "new-tls"},
	))
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Text != "[cyclops] 2 certificates need attention" {
		t.Errorf("fallback text = %q", m.Text)
	}
	if len(m.Blocks) != 4 { // header, report line, two findings
		t.Fatalf("want 4 blocks, got %d", len(m.Blocks))
	}
	if m.Blocks[0].Type != "header" || m.Blocks[0].Text.Type != "plain_text" {
		t.Errorf("first block = %+v, want a plain_text header", m.Blocks[0])
	}
	if got, want := m.Blocks[1].Text.Text, "Report `daily` · 2026-10-30 08:00 UTC"; got != want {
		t.Errorf("report line = %q, want %q", got, want)
	}
}

func TestBuildMessagesSplits(t *testing.T) {
	const total = findingsPerMessage*2 + 1
	rows := make([]render.Row, total)
	for i := range rows {
		rows[i] = render.Row{Kind: string(report.KindNeverIssued), Namespace: "ns", Name: fmt.Sprintf("c%d", i)}
	}
	msgs := buildMessages(testData(rows...))
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}

	seen := 0
	for i, m := range msgs {
		if len(m.Blocks) > maxBlocks {
			t.Errorf("message %d has %d blocks, over Slack's limit of %d", i, len(m.Blocks), maxBlocks)
		}
		if want := fmt.Sprintf("(part %d of 3)", i+1); !strings.Contains(m.Blocks[0].Text.Text, want) {
			t.Errorf("message %d header %q lacks %q", i, m.Blocks[0].Text.Text, want)
		}
		seen += len(m.Blocks) - 2
	}
	if seen != total {
		t.Errorf("findings across messages = %d, want %d", seen, total)
	}
	// Order is kept: the first finding of the last message follows the last of the one before.
	if got := msgs[2].Blocks[2].Text.Text; !strings.Contains(got, fmt.Sprintf("c%d", findingsPerMessage*2)) {
		t.Errorf("last message starts with %q", got)
	}
}

func TestBuildMessagesWelcome(t *testing.T) {
	data := testData()
	data.Welcome = true
	data.Schedule = "0 8 * * *"
	data.Namespaces = []string{"prod", "dev"}

	msgs := buildMessages(data)
	if len(msgs) != 1 || len(msgs[0].Blocks) != 2 {
		t.Fatalf("want one message of 2 blocks, got %+v", msgs)
	}
	body := msgs[0].Blocks[1].Text.Text
	for _, want := range []string{"`daily`", "`0 8 * * *` (UTC)", "namespaces prod, dev"} {
		if !strings.Contains(body, want) {
			t.Errorf("welcome text lacks %q:\n%s", want, body)
		}
	}
}

func TestSectionNeverExceedsSlackLimit(t *testing.T) {
	nasty := strings.Repeat("&<>", 500) // every character grows when escaped
	got := findingText(render.Row{Kind: string(report.KindNeverIssued), Namespace: "a", Name: "b", Reason: nasty, IssuerError: nasty})
	if n := utf8.RuneCountInString(section(got).Text.Text); n > maxSection {
		t.Errorf("section has %d characters, over %d", n, maxSection)
	}
}
