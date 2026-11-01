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

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
	"github.com/sakshitposting-irl/cyclops/internal/email"
	"github.com/sakshitposting-irl/cyclops/internal/render"
	"github.com/sakshitposting-irl/cyclops/internal/report"
	"github.com/sakshitposting-irl/cyclops/internal/webhooks"
)

// Values shared by the report tests.
const (
	testCyclopsNS  = "cyclops-system"
	testReportName = "prod-certs"
	testProd       = "prod"
	flagNamespace  = "--namespace=cyclops-system"
	flagCertReport = "--certreport=prod"
	testFrom       = "from@example.com"
	tmplCM         = "tmpl"
	keyGood        = "good"
	keyBad         = "bad"
)

func TestGenerateReport_MissingCertReportIsAnError(t *testing.T) {
	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()

	err = generateReport(context.Background(), c, nil, nil,
		reportOptions{CertReportName: "gone", Namespace: testCyclopsNS}, time.Now())
	if err == nil || !strings.Contains(err.Error(), `CertReport "gone" not found`) {
		t.Fatalf("generateReport() error = %v, want a not-found error naming the CertReport", err)
	}
}

func certificate(namespace, name string, notAfter *time.Time) *cmapi.Certificate {
	cert := &cmapi.Certificate{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	if notAfter != nil {
		cert.Status.NotAfter = &metav1.Time{Time: *notAfter}
	}
	return cert
}

func TestFindCertificates(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	expired := now.Add(-24 * time.Hour)
	healthy := now.Add(30 * 24 * time.Hour)

	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		certificate("a", "old", &expired),
		certificate("a", "fine", &healthy),
		certificate("b", "never", nil),
		certificate("c", "old-too", &expired),
	).Build()

	tests := map[string]struct {
		namespaces []string
		want       []string // "kind namespace/name", most urgent first
	}{
		"all namespaces": {
			want: []string{"NeverIssued b/never", "Expired a/old", "Expired c/old-too"},
		},
		"one namespace": {
			namespaces: []string{"a"},
			want:       []string{"Expired a/old"},
		},
		"a namespace that does not exist contributes nothing": {
			namespaces: []string{"missing"},
			want:       nil,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &cyclopsv1alpha1.CertReport{Spec: cyclopsv1alpha1.CertReportSpec{Namespaces: tt.namespaces}}
			findings, err := findCertificates(context.Background(), c, cr, now)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, f := range findings {
				got = append(got, kindName(f)+" "+f.Cert.Namespace+"/"+f.Cert.Name)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("findings = %v, want %v", got, tt.want)
			}
		})
	}
}

func kindName(f report.Finding) string { return string(f.Kind) }

// fakeSender records every message it is asked to send. fail, if set, can reject a message.
type fakeSender struct {
	sent []email.Message
	fail func(email.Message) error
}

func (f *fakeSender) Send(_ context.Context, msg email.Message) error {
	f.sent = append(f.sent, msg)
	if f.fail != nil {
		return f.fail(msg)
	}
	return nil
}

// fakeSlack records, per target, the reports it is asked to post. fail, if set, can reject one.
type fakeSlack struct {
	name string
	hub  *slackHub
}

// slackHub hands out fakeSlack senders and collects what they post, by target name.
type slackHub struct {
	sent    map[string][]render.TemplateData
	fail    func(name string) error
	missing map[string]bool // targets whose env var is "unset": the factory fails for them
}

func (f *fakeSlack) Send(_ context.Context, data render.TemplateData) error {
	f.hub.sent[f.name] = append(f.hub.sent[f.name], data)
	if f.hub.fail != nil {
		return f.hub.fail(f.name)
	}
	return nil
}

func (h *slackHub) factory(name string) (webhooks.Sender, error) {
	if h.missing[name] {
		return nil, fmt.Errorf("slack target %q: env var is empty", name)
	}
	return &fakeSlack{name: name, hub: h}, nil
}

// slackNotifier is a CertReport notifier of type Slack with one target per name.
func slackNotifier(names ...string) cyclopsv1alpha1.Notifier {
	n := cyclopsv1alpha1.Notifier{Type: cyclopsv1alpha1.NotifierTypeSlack, Slack: &cyclopsv1alpha1.SlackNotifier{}}
	for _, name := range names {
		n.Slack.Targets = append(n.Slack.Targets, cyclopsv1alpha1.SlackTarget{
			Name: name, URLSecretRef: cyclopsv1alpha1.SecretRef{Name: "slack-" + name},
		})
	}
	return n
}

// emailNotifier is a CertReport notifier of type Email.
func emailNotifier(to ...string) cyclopsv1alpha1.Notifier {
	return cyclopsv1alpha1.Notifier{
		Type:  cyclopsv1alpha1.NotifierTypeEmail,
		Email: &cyclopsv1alpha1.EmailNotifier{From: testFrom, To: to},
	}
}

// runFixture is a cluster with one healthy certificate, an optional expired one, and a
// CertReport with the given notifiers. newSender hands out sender and counts its calls.
type runFixture struct {
	c          client.Reader
	sender     *fakeSender
	newSender  senderFactory
	factoryHit int
	slack      *slackHub
}

func newRunFixture(t *testing.T, withExpired bool, notifiers ...cyclopsv1alpha1.Notifier) *runFixture {
	t.Helper()
	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	healthy := now.Add(30 * 24 * time.Hour)
	expired := now.Add(-24 * time.Hour)

	cr := &cyclopsv1alpha1.CertReport{
		ObjectMeta: metav1.ObjectMeta{Name: testReportName},
		Spec:       cyclopsv1alpha1.CertReportSpec{Schedule: "0 8 * * *", Notifiers: notifiers},
	}
	objs := []client.Object{cr, certificate("a", "fine", &healthy)}
	if withExpired {
		objs = append(objs, certificate("a", "old-cert", &expired))
	}

	f := &runFixture{
		c:      fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build(),
		sender: &fakeSender{},
		slack:  &slackHub{sent: map[string][]render.TemplateData{}},
	}
	f.newSender = func(context.Context) (email.Sender, error) {
		f.factoryHit++
		return f.sender, nil
	}
	return f
}

func (f *runFixture) run(welcome bool) error {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	opts := reportOptions{CertReportName: testReportName, Namespace: testCyclopsNS, Welcome: welcome}
	return generateReport(context.Background(), f.c, f.newSender, f.slack.factory, opts, now)
}

func TestGenerateReport(t *testing.T) {
	t.Run("a healthy day sends nothing and never builds the sender", func(t *testing.T) {
		f := newRunFixture(t, false, emailNotifier("to@example.com"))
		if err := f.run(false); err != nil {
			t.Fatalf("generateReport() error = %v, want nil", err)
		}
		if f.factoryHit != 0 || len(f.sender.sent) != 0 {
			t.Fatalf("factory called %d times, %d messages sent; want none (ADR 0015)", f.factoryHit, len(f.sender.sent))
		}
	})

	t.Run("findings are sent to the notifier", func(t *testing.T) {
		f := newRunFixture(t, true, emailNotifier("a@example.com", "b@example.com"))
		if err := f.run(false); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		if len(f.sender.sent) != 1 {
			t.Fatalf("sent %d messages, want 1", len(f.sender.sent))
		}
		msg := f.sender.sent[0]
		if msg.From != testFrom || !slices.Equal(msg.To, []string{"a@example.com", "b@example.com"}) {
			t.Fatalf("addressed %q -> %v", msg.From, msg.To)
		}
		if msg.Subject != "[cyclops] 1 certificate needs attention" || !strings.Contains(msg.HTMLBody, "old-cert") {
			t.Fatalf("subject %q, body:\n%s", msg.Subject, msg.HTMLBody)
		}
	})

	t.Run("one failing notifier does not stop the others, and the run fails", func(t *testing.T) {
		f := newRunFixture(t, true,
			emailNotifier("bad@example.com"),
			emailNotifier("good@example.com"))
		f.sender.fail = func(m email.Message) error {
			if m.To[0] == "bad@example.com" {
				return errors.New("mailbox unavailable")
			}
			return nil
		}
		err := f.run(false)
		if err == nil || !strings.Contains(err.Error(), "mailbox unavailable") ||
			!strings.Contains(err.Error(), "bad@example.com") {
			t.Fatalf("generateReport() error = %v, want it to name the failed notifier", err)
		}
		if len(f.sender.sent) != 2 {
			t.Fatalf("tried %d notifiers, want both", len(f.sender.sent))
		}
	})

	t.Run("every failure is reported, not just the first", func(t *testing.T) {
		f := newRunFixture(t, true,
			emailNotifier("one@example.com"),
			emailNotifier("two@example.com"))
		f.sender.fail = func(m email.Message) error { return errors.New("refused " + m.To[0]) }
		err := f.run(false)
		if err == nil || !strings.Contains(err.Error(), "refused one@") || !strings.Contains(err.Error(), "refused two@") {
			t.Fatalf("generateReport() error = %v, want both failures", err)
		}
	})

	t.Run("a sender that cannot be built fails the run", func(t *testing.T) {
		f := newRunFixture(t, true, emailNotifier("to@example.com"))
		f.newSender = func(context.Context) (email.Sender, error) { return nil, errors.New("bad SMTP config") }
		err := f.run(false)
		if err == nil || !strings.Contains(err.Error(), "bad SMTP config") {
			t.Fatalf("generateReport() error = %v, want the factory error", err)
		}
	})

	t.Run("an unsupported notifier type is an error, not a silent skip", func(t *testing.T) {
		f := newRunFixture(t, true, cyclopsv1alpha1.Notifier{Type: "Webhook"})
		err := f.run(false)
		if err == nil || !strings.Contains(err.Error(), `unsupported type "Webhook"`) {
			t.Fatalf("generateReport() error = %v, want an unsupported-type error", err)
		}
		if len(f.sender.sent) != 0 {
			t.Fatalf("sent %d messages, want none", len(f.sender.sent))
		}
	})
}

func TestGenerateReportSlack(t *testing.T) {
	t.Run("a healthy day posts nothing and builds no Slack sender", func(t *testing.T) {
		f := newRunFixture(t, false, slackNotifier("oncall"))
		if err := f.run(false); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		if len(f.slack.sent) != 0 {
			t.Fatalf("posted %v, want nothing (ADR 0015)", f.slack.sent)
		}
	})

	t.Run("findings go to every target, and a webhook-only report never builds the email sender", func(t *testing.T) {
		f := newRunFixture(t, true, slackNotifier("oncall", "platform-certs"))
		if err := f.run(false); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		for _, name := range []string{"oncall", "platform-certs"} {
			got := f.slack.sent[name]
			if len(got) != 1 || len(got[0].Findings) != 1 || got[0].Findings[0].Name != "old-cert" || got[0].Welcome {
				t.Fatalf("target %s got %+v, want one report with old-cert", name, got)
			}
		}
		if f.factoryHit != 0 {
			t.Fatalf("email sender built %d times, want 0", f.factoryHit)
		}
	})

	t.Run("email and Slack together both receive the report", func(t *testing.T) {
		f := newRunFixture(t, true, emailNotifier("to@example.com"), slackNotifier("oncall"))
		if err := f.run(false); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		if len(f.sender.sent) != 1 || len(f.slack.sent["oncall"]) != 1 {
			t.Fatalf("email sent %d, slack posted %d; want 1 each", len(f.sender.sent), len(f.slack.sent["oncall"]))
		}
	})

	t.Run("one failing target does not stop the others, and the run fails naming it", func(t *testing.T) {
		f := newRunFixture(t, true, slackNotifier("broken", "oncall"), emailNotifier("to@example.com"))
		f.slack.fail = func(name string) error {
			if name == "broken" {
				return errors.New(`slack target "broken": status 404`)
			}
			return nil
		}
		err := f.run(false)
		if err == nil || !strings.Contains(err.Error(), `slack target "broken"`) {
			t.Fatalf("generateReport() error = %v, want it to name the failed target", err)
		}
		if len(f.slack.sent["oncall"]) != 1 || len(f.sender.sent) != 1 {
			t.Fatalf("the other target and the email must still be sent: slack %v, email %d", f.slack.sent, len(f.sender.sent))
		}
	})

	t.Run("a target whose URL is missing fails, and the others still post", func(t *testing.T) {
		f := newRunFixture(t, true, slackNotifier("unset", "oncall"))
		f.slack.missing = map[string]bool{"unset": true}
		err := f.run(false)
		if err == nil || !strings.Contains(err.Error(), `slack target "unset"`) {
			t.Fatalf("generateReport() error = %v, want it to name the target", err)
		}
		if len(f.slack.sent["oncall"]) != 1 {
			t.Fatalf("oncall got %d posts, want 1", len(f.slack.sent["oncall"]))
		}
	})

	t.Run("type Slack without a slack block is an error", func(t *testing.T) {
		f := newRunFixture(t, true, cyclopsv1alpha1.Notifier{Type: cyclopsv1alpha1.NotifierTypeSlack})
		if err := f.run(false); err == nil || !strings.Contains(err.Error(), "has no slack block") {
			t.Fatalf("generateReport() error = %v, want a missing-block error", err)
		}
	})

	t.Run("the welcome run posts the welcome to every target", func(t *testing.T) {
		f := newRunFixture(t, false, slackNotifier("oncall"))
		if err := f.run(true); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		got := f.slack.sent["oncall"]
		if len(got) != 1 || !got[0].Welcome || got[0].Schedule != "0 8 * * *" {
			t.Fatalf("posted %+v, want one welcome with the schedule", got)
		}
	})
}

func TestGenerateReportWelcome(t *testing.T) {
	t.Run("the welcome run sends even with no findings", func(t *testing.T) {
		f := newRunFixture(t, false, emailNotifier("to@example.com"))
		if err := f.run(true); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		if len(f.sender.sent) != 1 || f.sender.sent[0].Subject != "[cyclops] Welcome! Let's get you started" {
			t.Fatalf("sent %+v, want one welcome message", f.sender.sent)
		}
	})

	t.Run("the welcome email has its own template: schedule and namespaces, and no findings", func(t *testing.T) {
		f := newRunFixture(t, true, emailNotifier("to@example.com")) // an expired certificate exists
		if err := f.run(true); err != nil {
			t.Fatalf("generateReport() error = %v", err)
		}
		if len(f.sender.sent) != 1 {
			t.Fatalf("sent %d messages, want 1", len(f.sender.sent))
		}
		body := f.sender.sent[0].HTMLBody
		if !strings.Contains(body, "Welcome to Cyclops") || !strings.Contains(body, "0 8 * * *") {
			t.Fatalf("not the welcome template, or the schedule is missing:\n%s", body)
		}
		if strings.Contains(body, "old-cert") {
			t.Fatalf("the welcome email must not list findings:\n%s", body)
		}
		if len(f.sender.sent[0].Inline) != 1 {
			t.Fatalf("want the logo attached, got %d inline images", len(f.sender.sent[0].Inline))
		}
	})
}

func TestLoadTemplate(t *testing.T) {
	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: tmplCM, Namespace: testCyclopsNS},
		Data: map[string]string{
			keyGood: "custom for {{ .ReportName }}",
			keyBad:  "{{ .ReportName ",
			// the chart-wide default (--default-template-configmap) is read from this key
			defaultTemplateKey: "default for {{ .ReportName }}",
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()

	reportWith := func(name, key string) *cyclopsv1alpha1.CertReport {
		cr := &cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: testReportName}}
		if name != "" {
			cr.Spec.EmailTemplateConfigMapRef = &cyclopsv1alpha1.TemplateConfigMapRef{Name: name, Key: key}
		}
		return cr
	}

	t.Run("no reference uses the built-in template", func(t *testing.T) {
		got, err := loadTemplate(context.Background(), c, reportWith("", ""), testCyclopsNS, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != render.Default {
			t.Fatal("want render.Default")
		}
	})

	t.Run("a custom template is read from the ConfigMap and parsed", func(t *testing.T) {
		got, err := loadTemplate(context.Background(), c, reportWith(tmplCM, keyGood), testCyclopsNS, "")
		if err != nil {
			t.Fatal(err)
		}
		body, err := render.Render(got, render.TemplateData{ReportName: testProd})
		if err != nil {
			t.Fatal(err)
		}
		if body != "custom for "+testProd {
			t.Fatalf("body = %q, want %q", body, "custom for "+testProd)
		}
	})

	t.Run("the chart-wide default applies when the CertReport has no reference", func(t *testing.T) {
		got, err := loadTemplate(context.Background(), c, reportWith("", ""), testCyclopsNS, tmplCM)
		if err != nil {
			t.Fatal(err)
		}
		body, err := render.Render(got, render.TemplateData{ReportName: testProd})
		if err != nil {
			t.Fatal(err)
		}
		if body != "default for "+testProd {
			t.Fatalf("body = %q, want %q", body, "default for "+testProd)
		}
	})

	t.Run("a CertReport's own reference beats the chart-wide default", func(t *testing.T) {
		got, err := loadTemplate(context.Background(), c, reportWith(tmplCM, keyGood), testCyclopsNS, tmplCM)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := render.Render(got, render.TemplateData{ReportName: testProd})
		if body != "custom for "+testProd {
			t.Fatalf("body = %q, want %q", body, "custom for "+testProd)
		}
	})

	t.Run("a missing chart-wide default ConfigMap fails the run", func(t *testing.T) {
		_, err := loadTemplate(context.Background(), c, reportWith("", ""), testCyclopsNS, "nope")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("loadTemplate() error = %v, want not found", err)
		}
	})

	errCases := map[string]struct {
		name, key, namespace, want string
	}{
		"missing ConfigMap": {"nope", keyGood, testCyclopsNS, "not found"},
		"missing key":       {tmplCM, "absent", testCyclopsNS, `no key "absent"`},
		"template syntax":   {tmplCM, keyBad, testCyclopsNS, "parsing template"},
		"other namespace":   {tmplCM, keyGood, "elsewhere", "not found"},
	}
	for name, tt := range errCases {
		t.Run(name, func(t *testing.T) {
			_, err := loadTemplate(context.Background(), c, reportWith(tt.name, tt.key), tt.namespace, "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("loadTemplate() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoadWelcomeTemplate(t *testing.T) {
	s, err := newReportScheme()
	if err != nil {
		t.Fatal(err)
	}
	const (
		withWelcomeCM = "with-welcome"
		badWelcomeCM  = "bad-welcome"
	)
	cmWith := func(name string, data map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testCyclopsNS}, Data: data}
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		cmWith(tmplCM, map[string]string{keyGood: "report for {{ .ReportName }}"}), // no welcome key
		cmWith(withWelcomeCM, map[string]string{
			keyGood:                   "report for {{ .ReportName }}",
			render.WelcomeTemplateKey: "welcome for {{ .ReportName }}",
		}),
		cmWith(badWelcomeCM, map[string]string{keyGood: "ok", render.WelcomeTemplateKey: "{{ .ReportName "}),
	).Build()

	reportWith := func(name string) *cyclopsv1alpha1.CertReport {
		cr := &cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: testReportName}}
		if name != "" {
			cr.Spec.EmailTemplateConfigMapRef = &cyclopsv1alpha1.TemplateConfigMapRef{Name: name, Key: keyGood}
		}
		return cr
	}
	ctx := context.Background()

	t.Run("no reference uses the built-in welcome template", func(t *testing.T) {
		got, err := loadWelcomeTemplate(ctx, c, reportWith(""), testCyclopsNS, "")
		if err != nil || got != render.DefaultWelcome {
			t.Fatalf("got %v, %v; want render.DefaultWelcome", got, err)
		}
	})

	t.Run("a ConfigMap without the welcome key falls back to the built-in one", func(t *testing.T) {
		got, err := loadWelcomeTemplate(ctx, c, reportWith(tmplCM), testCyclopsNS, "")
		if err != nil || got != render.DefaultWelcome {
			t.Fatalf("got %v, %v; want render.DefaultWelcome", got, err)
		}
	})

	t.Run("the welcome key is read from the same ConfigMap and parsed", func(t *testing.T) {
		got, err := loadWelcomeTemplate(ctx, c, reportWith(withWelcomeCM), testCyclopsNS, "")
		if err != nil {
			t.Fatal(err)
		}
		body, err := render.Render(got, render.TemplateData{ReportName: testReportName})
		if err != nil || body != "welcome for "+testReportName {
			t.Fatalf("body = %q, err = %v", body, err)
		}
	})

	t.Run("the chart-wide default ConfigMap supplies the welcome template too", func(t *testing.T) {
		got, err := loadWelcomeTemplate(ctx, c, reportWith(""), testCyclopsNS, withWelcomeCM)
		if err != nil || got == render.DefaultWelcome {
			t.Fatalf("got %v, %v; want the custom welcome template", got, err)
		}
	})

	t.Run("a welcome template that does not parse fails the run", func(t *testing.T) {
		_, err := loadWelcomeTemplate(ctx, c, reportWith(badWelcomeCM), testCyclopsNS, "")
		if err == nil || !strings.Contains(err.Error(), render.WelcomeTemplateKey) {
			t.Fatalf("error = %v, want one naming %q", err, render.WelcomeTemplateKey)
		}
	})

	t.Run("a missing ConfigMap fails the run, as for the report", func(t *testing.T) {
		_, err := loadWelcomeTemplate(ctx, c, reportWith("nope"), testCyclopsNS, "")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("error = %v, want not found", err)
		}
	})
}

func TestRenderReport(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	expired := now.Add(-24 * time.Hour)
	findings := []report.Finding{{
		Cert: report.CertStatus{Namespace: "a", Name: "old-cert", NotAfter: expired},
		Kind: report.KindExpired,
	}}

	t.Run("the built-in template lists the findings", func(t *testing.T) {
		subject, body, err := renderReport(render.Default, render.NewTemplateData(testReportName, now, findings))
		if err != nil {
			t.Fatal(err)
		}
		if subject != "[cyclops] 1 certificate needs attention" {
			t.Fatalf("subject = %q", subject)
		}
		if !strings.Contains(body, "old-cert") {
			t.Fatalf("body does not mention the certificate:\n%s", body)
		}
	})

	t.Run("a custom template gets the report name and rows", func(t *testing.T) {
		tmpl, err := render.Parse("{{ .ReportName }}: {{ range .Findings }}{{ .Namespace }}/{{ .Name }} {{ .Kind }}{{ end }}")
		if err != nil {
			t.Fatal(err)
		}
		_, body, err := renderReport(tmpl, render.NewTemplateData(testReportName, now, findings))
		if err != nil {
			t.Fatal(err)
		}
		if body != "prod-certs: a/old-cert Expired" {
			t.Fatalf("body = %q", body)
		}
	})

	t.Run("a template naming an unknown field fails with no body", func(t *testing.T) {
		tmpl, err := render.Parse("{{ .Findngs }}")
		if err != nil {
			t.Fatal(err) // parses fine: the typo only shows up when it runs
		}
		subject, body, err := renderReport(tmpl, render.NewTemplateData(testReportName, now, findings))
		if err == nil {
			t.Fatal("want an error for the unknown field")
		}
		if subject != "" || body != "" {
			t.Fatalf("got subject %q and body %q, want both empty on error", subject, body)
		}
	})
}

func TestParseReportFlags(t *testing.T) {
	tests := map[string]struct {
		args    []string
		want    reportOptions
		wantErr string
	}{
		"all flags": {
			args: []string{flagCertReport, flagNamespace, "--welcome"},
			want: reportOptions{CertReportName: testProd, Namespace: "cyclops-system", Welcome: true},
		},
		"welcome defaults to false": {
			args: []string{flagCertReport, flagNamespace},
			want: reportOptions{CertReportName: testProd, Namespace: "cyclops-system"},
		},
		"missing certreport": {args: []string{flagNamespace}, wantErr: "--certreport is required"},
		"missing namespace":  {args: []string{flagCertReport}, wantErr: "--namespace is required"},
		"no flags":           {args: nil, wantErr: "--certreport is required"},
		"unknown flag":       {args: []string{"--bogus"}, wantErr: "bogus"},
		"stray argument": {
			args:    []string{flagCertReport, flagNamespace, "extra"},
			wantErr: "unexpected arguments",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseReportFlags(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseReportFlags() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("parseReportFlags() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
