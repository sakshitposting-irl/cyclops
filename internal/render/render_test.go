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

package render

import (
	"html/template"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// now is fixed, and deliberately not UTC, to check the UTC conversion.
var now = time.Date(2026, 9, 25, 18, 30, 0, 0, time.FixedZone("IST", 5*3600+1800))

// Test values that appear in several cases.
const (
	testReport = "daily"
	testBroken = "broken"
	testReason = "no CA"
	testProd   = "prod"
	testDev    = "dev"
	testWeb    = "web"
	testNever  = "NeverIssued"
)

func days(n float64) time.Duration { return time.Duration(n * 24 * float64(time.Hour)) }

func TestDaysLeft(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want int
	}{
		{"ten days", days(10), 10},
		{"most of a day rounds down", days(0.9), 0},
		{"exactly now", 0, 0},
		{"half a day expired rounds down to -1", days(-0.5), -1},
		{"one day expired", days(-1), -1},
		{"a day and a half expired", days(-1.5), -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := daysLeft(now.Add(tt.in), now); got != tt.want {
				t.Errorf("daysLeft(now%+v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewTemplateData(t *testing.T) {
	findings := []report.Finding{
		{Kind: report.KindNeverIssued, Cert: report.CertStatus{
			Namespace: testDev, Name: testBroken, FailedIssuanceAttempts: 2,
			State: "errored", Reason: testReason,
		}},
		{Kind: report.KindRenewalOverdue, Cert: report.CertStatus{
			Namespace: testProd, Name: testWeb,
			NotAfter: now.Add(days(5)), RenewalTime: now.Add(days(-10)),
		}},
	}

	got := NewTemplateData(testReport, now, findings)

	want := TemplateData{
		ReportName:  testReport,
		GeneratedAt: now.UTC(),
		Findings: []Row{
			{
				Kind: "NeverIssued", Namespace: testDev, Name: testBroken,
				FailedIssuanceAttempts: 2, State: "errored", Reason: testReason,
			},
			{
				Kind: "RenewalOverdue", Namespace: testProd, Name: testWeb,
				NotAfter: now.Add(days(5)).UTC(), DaysLeft: 5,
				RenewalTime: now.Add(days(-10)).UTC(),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewTemplateData() =\n  %+v\nwant\n  %+v", got, want)
	}
	// DeepEqual compares time zones too, but say it explicitly: the email
	// must not depend on the Job's timezone (ADR 0013).
	if loc := got.GeneratedAt.Location(); loc != time.UTC {
		t.Errorf("GeneratedAt location = %v, want UTC", loc)
	}
}

func TestRender_Default(t *testing.T) {
	data := TemplateData{
		ReportName:  testReport,
		GeneratedAt: now.UTC(),
		Findings: []Row{
			{Kind: "NeverIssued", Namespace: testDev, Name: testBroken, Reason: testReason},
			{
				Kind: "RenewalOverdue", Namespace: testProd, Name: testWeb,
				NotAfter: now.Add(days(5)).UTC(), DaysLeft: 5,
				RenewalTime: now.Add(days(-10)).UTC(),
				State:       "pending", Reason: `Waiting for <b>HTTP-01</b> & "friends"`,
			},
		},
	}

	body, err := Render(Default, data)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	for _, want := range []string{
		// Without it, clients guess Windows-1252 and "—" shows as "â€”".
		`<meta charset="utf-8">`,
		testReport,
		"2 certificates need attention",
		"dev/broken", "never issued", "Never issued",
		"prod/web", "Renewal overdue", "2026-09-30", "5 days left",
		// Untrusted Reason text is escaped, not rendered as markup (ADR 0005).
		"Waiting for &lt;b&gt;HTTP-01&lt;/b&gt; &amp; &#34;friends&#34;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	for _, unwanted := range []string{"<b>HTTP-01</b>", "0001-01-01"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body contains %q", unwanted)
		}
	}
}

func TestRender_DefaultIngresses(t *testing.T) {
	data := TemplateData{
		ReportName:  testReport,
		GeneratedAt: now.UTC(),
		Findings: []Row{
			{Kind: testNever, Namespace: testDev, Name: testBroken, Ingresses: []string{"ingress-prod", "ingress-qa"}},
			{Kind: testNever, Namespace: testProd, Name: testWeb},
		},
	}
	body, err := Render(Default, data)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(body, "Ingress: ingress-prod, ingress-qa") {
		t.Errorf("body missing the Ingress list")
	}
	if n := strings.Count(body, "Ingress:"); n != 1 {
		t.Errorf("Ingress label appears %d times, want 1 (a row without Ingresses shows none)", n)
	}
}

func TestRender_DefaultEmpty(t *testing.T) {
	body, err := Render(Default, TemplateData{ReportName: testReport, GeneratedAt: now.UTC()})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(body, "No certificates need attention") {
		t.Errorf("empty report body missing the all-clear message:\n%s", body)
	}
}

func TestRender_Custom(t *testing.T) {
	tmpl, err := Parse(`{{ .ReportName }}: {{ range .Findings }}[{{ .Name }}]{{ end }}`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	body, err := Render(tmpl, TemplateData{
		ReportName: testReport,
		Findings:   []Row{{Name: "a"}, {Name: "b"}},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if want := "daily: [a][b]"; body != want {
		t.Errorf("Render() = %q, want %q", body, want)
	}
}

func TestParse_Error(t *testing.T) {
	if _, err := Parse(`{{ .ReportName `); err == nil {
		t.Error("Parse() of an unterminated action: want error, got nil")
	}
}

// A template that parses but fails while running (here: a field that
// doesn't exist) must return an error and no body, never half an email.
func TestRender_ExecuteError(t *testing.T) {
	tmpl, err := Parse(`before {{ .NoSuchField }} after`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	body, err := Render(tmpl, TemplateData{})
	if err == nil {
		t.Error("Render(): want error for unknown field, got nil")
	}
	if body != "" {
		t.Errorf("Render() body = %q, want empty on error", body)
	}
}

// A nil or never-parsed template makes Execute panic, which would crash the
// report Job; Render must turn it into an error.
func TestRender_NilTemplate(t *testing.T) {
	for name, tmpl := range map[string]*template.Template{
		"nil":        nil,
		"not parsed": new(template.Template),
	} {
		t.Run(name, func(t *testing.T) {
			body, err := Render(tmpl, TemplateData{})
			if err == nil {
				t.Error("Render(): want error, got nil")
			}
			if body != "" {
				t.Errorf("Render() body = %q, want empty on error", body)
			}
		})
	}
}

// A zero TemplateData must not print year-1 dates.
func TestRender_DefaultZeroData(t *testing.T) {
	body, err := Render(Default, TemplateData{})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(body, "0001-01-01") {
		t.Errorf("body contains a zero date:\n%s", body)
	}
}

func TestSubject(t *testing.T) {
	tests := []struct {
		findings int
		want     string
	}{
		{0, "[cyclops] All certificates healthy"},
		{1, "[cyclops] 1 certificate needs attention"},
		{3, "[cyclops] 3 certificates need attention"},
	}
	for _, tt := range tests {
		data := TemplateData{Findings: make([]Row, tt.findings)}
		if got := Subject(data); got != tt.want {
			t.Errorf("Subject(%d findings) = %q, want %q", tt.findings, got, tt.want)
		}
	}
}

func TestInlineImages(t *testing.T) {
	body, err := Render(Default, TemplateData{ReportName: testReport})
	if err != nil {
		t.Fatal(err)
	}
	got := InlineImages(body)
	if len(got) != 1 || got[0].ContentID != "cyclops-logo" || got[0].ContentType != "image/png" {
		t.Fatalf("default body should attach the logo, got %+v", got)
	}
	// The embedded file really is a PNG, so a bad copy can't ship a broken image.
	if !strings.HasPrefix(string(got[0].Data), "\x89PNG") {
		t.Errorf("logo data is not a PNG")
	}

	if got := InlineImages("<p>custom template without the logo</p>"); got != nil {
		t.Errorf("a body that doesn't use the logo should attach nothing, got %+v", got)
	}
}

func TestRender_DefaultWelcome(t *testing.T) {
	data := TemplateData{
		ReportName:  testReport,
		GeneratedAt: now.UTC(),
		Welcome:     true,
		Schedule:    "0 8 * * *",
		Namespaces:  []string{testProd, testDev},
		// The welcome email never lists certificates, even when some need attention.
		Findings: []Row{{Kind: testNever, Namespace: testWeb, Name: "secret-cert"}},
	}
	body, err := Render(DefaultWelcome, data)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, want := range []string{"Welcome to Cyclops: daily", "<code>0 8 * * *</code> (UTC)", "namespaces prod, dev", "cid:cyclops-logo"} {
		if !strings.Contains(body, want) {
			t.Errorf("welcome body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "secret-cert") {
		t.Errorf("welcome body must not list findings:\n%s", body)
	}
	if got := InlineImages(body); len(got) != 1 {
		t.Errorf("welcome body should attach the logo, got %d images", len(got))
	}
}

func TestRender_DefaultWelcomeAllNamespaces(t *testing.T) {
	body, err := Render(DefaultWelcome, TemplateData{ReportName: testReport, Welcome: true})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(body, "every namespace") {
		t.Errorf("empty Namespaces should say every namespace:\n%s", body)
	}
	if strings.Contains(body, "Checks run") {
		t.Errorf("no Schedule should omit the schedule row:\n%s", body)
	}
}
