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
	"flag"
	"fmt"
	"html/template"
	"os"
	"strings"
	"time"

	acmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
	"github.com/sakshitposting-irl/cyclops/internal/certmanager"
	"github.com/sakshitposting-irl/cyclops/internal/email"
	"github.com/sakshitposting-irl/cyclops/internal/render"
	"github.com/sakshitposting-irl/cyclops/internal/report"
)

// Report mode (ADR 0006). The CronJob's Pod runs this same binary as
// `cyclops report --certreport=<name> --namespace=<ns> [--welcome]`. It is a one-shot: do the
// work, exit. There is no manager, no leader election and no webhook here, and exiting
// non-zero is how the Job (and so Kubernetes) learns the run failed.
//
// It is split in two so the interesting part can be tested without a cluster:
//   - runReport does the process plumbing (flags, logger, client) and calls
//   - generateReport, which takes its dependencies as arguments (a fake client and a fake
//     sender factory in tests) and does the actual work.

// reportOptions are the flags of `cyclops report`.
type reportOptions struct {
	// CertReportName is the CertReport this run is for (--certreport, required).
	CertReportName string
	// Namespace is the cyclops namespace, where a custom template ConfigMap lives
	// (--namespace, required; the controller passes it).
	Namespace string
	// Welcome marks the one-off welcome run (--welcome, ADR 0015): send even when nothing
	// needs attention, so a new CertReport proves delivery works.
	Welcome bool
	// DefaultTemplateConfigMap names a ConfigMap (key defaultTemplateKey) in Namespace used for
	// every CertReport that sets no emailTemplateConfigMapRef (--default-template-configmap;
	// the controller passes it, from the chart's emailTemplate.useAsDefault). Empty means the
	// built-in template.
	DefaultTemplateConfigMap string
}

// defaultTemplateKey is the ConfigMap key of the chart-wide default template. The Helm chart
// writes the template under this key.
const defaultTemplateKey = render.DefaultTemplateKey

// parseReportFlags parses args (everything after the word "report") into its own FlagSet,
// like run does, so tests can call it repeatedly with different args.
func parseReportFlags(args []string) (reportOptions, error) {
	flagSet := flag.NewFlagSet("report", flag.ContinueOnError)
	var opts reportOptions
	flagSet.StringVar(&opts.CertReportName, "certreport", "", "CertReport name (required)")
	flagSet.StringVar(&opts.Namespace, "namespace", "", "Namespace where cyclops is installed (required)")
	flagSet.BoolVar(&opts.Welcome, "welcome", false,
		"Send even when nothing needs attention (one-off welcome run)")

	flagSet.StringVar(&opts.DefaultTemplateConfigMap, "default-template-configmap", "",
		"ConfigMap whose "+defaultTemplateKey+" key is the template for CertReports without their own (optional)")

	if err := flagSet.Parse(args); err != nil {
		return reportOptions{}, err
	}
	if flagSet.NArg() > 0 {
		return reportOptions{}, fmt.Errorf("unexpected arguments: %v", flagSet.Args())
	}
	if opts.CertReportName == "" {
		return reportOptions{}, errors.New("--certreport is required")
	}
	if opts.Namespace == "" {
		return reportOptions{}, errors.New("--namespace is required")
	}
	return opts, nil
}

// senderFactory builds the email Sender. generateReport calls it only when it has something
// to send, so a healthy day never touches the mail configuration (ADR 0015) and tests can
// pass a fake.
type senderFactory func(ctx context.Context) (email.Sender, error)

// newReportScheme is the scheme report mode reads with: core types (the template ConfigMap),
// the CertReport, and the cert-manager and ACME types certmanager.List reads. The manager's
// scheme in main.go lacks the cert-manager types, hence a separate one.
func newReportScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		cyclopsv1alpha1.AddToScheme,
		cmapi.AddToScheme,
		acmev1.AddToScheme,
	} {
		if err := add(s); err != nil {
			return nil, fmt.Errorf("building scheme: %w", err)
		}
	}
	return s, nil
}

// runReport is the entry point for report mode. It returns an error on any failure so that
// main() exits 1 and the Job is marked failed.
func runReport(ctx context.Context, args []string) error {
	// Install a real logger before anything can fail: ctrl.Log is only a handle and prints
	// nothing until SetLogger is called, and main reports every returned error through it. Set
	// after flag parsing, a missing --certreport would exit 1 with no message at all.
	ctrl.SetLogger(zap.New())

	opts, err := parseReportFlags(args)
	if err != nil {
		return err
	}

	// Put a logger carrying this run's identity into the context, so generateReport can log
	// with ctrl.LoggerFrom(ctx).
	log := ctrl.Log.WithName("report").WithValues(
		"certreport", opts.CertReportName, "namespace", opts.Namespace, "welcome", opts.Welcome)
	ctx = ctrl.LoggerInto(ctx, log)

	reportScheme, err := newReportScheme()
	if err != nil {
		return err
	}
	// GetConfig, not GetConfigOrDie: the latter calls os.Exit, which skips main's error
	// handling. A plain client with no cache fits a one-shot that reads each thing once.
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}
	c, err := client.New(restConfig, client.Options{Scheme: reportScheme})
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}

	// The Pod's mail settings come from env vars (ADR 0014); report mode reads nothing else.
	newSender := func(ctx context.Context) (email.Sender, error) {
		return email.FromEnv(ctx, os.Getenv)
	}
	return generateReport(ctx, c, newSender, opts, time.Now())
}

// generateReport is one report run. now is passed in so tests control the clock.
//
// The steps, in order:
//  1. Load the CertReport.
//  2. List the certificates in scope and evaluate them into findings.
//  3. Exit quietly if there are none, unless this is the welcome run (ADR 0015).
//  4. Load the template and render the subject and body.
//  5. Build the sender and deliver to every notifier.
//
// A failure at any step returns an error, so the Job fails and Kubernetes records the run as
// failed.
//
// TODO(adr0015): render.TemplateData has no field yet to tell the template this is the
// welcome run (policy text plus current findings). Adding one is additive (ADR 0013), but it
// belongs to the welcome-email work.
func generateReport(
	ctx context.Context, c client.Reader, newSender senderFactory, opts reportOptions, now time.Time,
) error {
	log := ctrl.LoggerFrom(ctx)

	// The CertReport is cluster-scoped, so the key is just its name. If it is gone the
	// CronJob outlived it, which should not happen (the CertReport owns the CronJob), so fail
	// the run loudly instead of exiting quietly.
	var cr cyclopsv1alpha1.CertReport
	if err := c.Get(ctx, client.ObjectKey{Name: opts.CertReportName}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("CertReport %q not found: its CronJob should have been deleted with it", opts.CertReportName)
		}
		return fmt.Errorf("getting CertReport %q: %w", opts.CertReportName, err)
	}
	log.Info("loaded CertReport", "namespaces", cr.Spec.Namespaces, "notifiers", len(cr.Spec.Notifiers))

	findings, err := findCertificates(ctx, c, &cr, now)
	if err != nil {
		return err
	}
	log.Info("evaluated certificates", "findings", len(findings))

	// ADR 0015: a healthy day sends nothing. Return before newSender is called, so a quiet
	// day never touches the mail settings and cannot fail because of them. The welcome run
	// is the exception: it sends even with no findings, to prove delivery works.
	if len(findings) == 0 && !opts.Welcome {
		log.Info("nothing to report")
		return nil
	}

	// The template is loaded after the early exit, so a quiet day never reads the ConfigMap. A
	// bad custom template fails the run here, before anything is sent.
	tmpl, err := loadTemplate(ctx, c, &cr, opts.Namespace, opts.DefaultTemplateConfigMap)
	if err != nil {
		return err
	}
	subject, body, err := renderReport(tmpl, cr.Name, now, findings)
	if err != nil {
		return err
	}
	log.Info("rendered report", "subject", subject, "bytes", len(body))

	// Only now, with something to send, are the mail settings read. A factory failure is an
	// error: there is a report and no way to deliver it.
	sender, err := newSender(ctx)
	if err != nil {
		return fmt.Errorf("building the email sender: %w", err)
	}
	return sendToNotifiers(ctx, sender, cr.Spec.Notifiers, subject, body)
}

// sendToNotifiers delivers one rendered report to every notifier. It does not stop at the
// first failure: one bad recipient list must not silence the others. All failures are
// returned joined, so the Job fails and every cause is in its log. A notifier that is not
// understood (a type added to the schema before this binary supports it) is an error too,
// not a silent skip, so a report is never dropped without anyone knowing.
func sendToNotifiers(
	ctx context.Context, sender email.Sender, notifiers []cyclopsv1alpha1.Notifier, subject, body string,
) error {
	log := ctrl.LoggerFrom(ctx)
	var errs []error
	for i, n := range notifiers {
		switch {
		case n.Type == cyclopsv1alpha1.NotifierTypeEmail && n.Email != nil:
			msg := email.Message{From: n.Email.From, To: n.Email.To, Subject: subject, HTMLBody: body}
			if err := sender.Send(ctx, msg); err != nil {
				errs = append(errs, fmt.Errorf("notifier %d (Email to %s): %w", i, strings.Join(n.Email.To, ", "), err))
				continue
			}
			log.Info("report sent", "notifier", i, "to", n.Email.To)
		case n.Type == cyclopsv1alpha1.NotifierTypeEmail:
			errs = append(errs, fmt.Errorf("notifier %d: type Email has no email block", i))
		default:
			errs = append(errs, fmt.Errorf("notifier %d: unsupported type %q", i, n.Type))
		}
	}
	return errors.Join(errs...)
}

// renderReport builds the email subject and HTML body for the findings. The subject is set
// in code and the body comes from tmpl (ADR 0013). A template that fails to run, for example
// one naming a field that does not exist, returns an error and no body, so a broken template
// never sends half an email.
func renderReport(
	tmpl *template.Template, reportName string, now time.Time, findings []report.Finding,
) (subject, body string, err error) {
	data := render.NewTemplateData(reportName, now, findings)
	body, err = render.Render(tmpl, data)
	if err != nil {
		return "", "", fmt.Errorf("rendering the report: %w", err)
	}
	return render.Subject(data), body, nil
}

// loadTemplate returns the email template for cr: the built-in one, or the custom one named
// by spec.emailTemplateConfigMapRef, read from the ConfigMap of that name in namespace (the
// cyclops namespace, ADR 0018). A missing ConfigMap, a missing key or a template that does
// not parse is an error, so a broken custom template fails the run instead of sending
// nothing or a half-rendered email (ADR 0005).
//
// A CertReport without its own reference uses the ConfigMap named by defaultConfigMap (the
// chart-wide default, ADR 0005 amendment), or the built-in template if that is empty too.
func loadTemplate(
	ctx context.Context, c client.Reader, cr *cyclopsv1alpha1.CertReport, namespace, defaultConfigMap string,
) (*template.Template, error) {
	ref := cr.Spec.EmailTemplateConfigMapRef
	if ref == nil && defaultConfigMap != "" {
		ref = &cyclopsv1alpha1.TemplateConfigMapRef{Name: defaultConfigMap, Key: defaultTemplateKey}
	}
	if ref == nil {
		return render.Default, nil
	}

	var cm corev1.ConfigMap
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("template ConfigMap %s/%s not found", namespace, ref.Name)
		}
		return nil, fmt.Errorf("reading template ConfigMap %s/%s: %w", namespace, ref.Name, err)
	}
	text, ok := cm.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("template ConfigMap %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	tmpl, err := render.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parsing template %s/%s key %q: %w", namespace, ref.Name, ref.Key, err)
	}
	return tmpl, nil
}

// findCertificates reads the certificates in namespaces (empty means all, ADR 0010) and
// returns the ones that need attention as of now, most urgent first (ADR 0012). Failure
// diagnostics are read as well, but they only decorate findings and never add one (ADR 0009).
func findCertificates(
	ctx context.Context, c client.Reader, cr *cyclopsv1alpha1.CertReport, now time.Time,
) ([]report.Finding, error) {
	var snap certmanager.Snapshot
	var err error
	if cr.Spec.IngressAnnotationMode {
		// ADR 0020: scope is the Certificates behind annotated Ingresses.
		snap, err = certmanager.ListForIngresses(ctx, c, cr.Name, cyclopsv1alpha1.AnnotationCRName)
	} else {
		snap, err = certmanager.List(ctx, c, cr.Spec.Namespaces)
	}
	if err != nil {
		return nil, fmt.Errorf("listing certificates: %w", err)
	}
	return report.Evaluate(snap.CertStatuses(), now), nil
}
