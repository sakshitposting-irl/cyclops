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

package controller

import (
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

// These tests check what the CertReport CRD accepts and rejects (ADR 0018). They need no
// reconciler: k8sClient (suite_test.go) talks to envtest's real API server, which has the
// CRD installed and enforces its schema, defaults and CEL rule.
//
// Every Create uses client.DryRunAll, so the server validates the object without storing
// it. Nothing is left behind and tests can't clash on the cluster-scoped name.

const (
	samplePath = "../../config/samples/cyclops_v1alpha1_certreport.yaml"
	// testNS is a valid namespace name used where the name itself does not matter.
	testNS = "prod"
)

// validCertReport returns a CertReport the CRD accepts. Each test starts from this and
// breaks exactly one thing, so a failure points at the rule being tested.
func validCertReport() *cyclopsv1alpha1.CertReport {
	return &cyclopsv1alpha1.CertReport{
		ObjectMeta: metav1.ObjectMeta{Name: "crd-test"},
		Spec: cyclopsv1alpha1.CertReportSpec{
			Notifiers: []cyclopsv1alpha1.Notifier{{
				Type: cyclopsv1alpha1.NotifierTypeEmail,
				Email: &cyclopsv1alpha1.EmailNotifier{
					From: "cyclops@example.com",
					To:   []string{testRecipient},
				},
			}},
		},
	}
}

// namesN returns n distinct, valid namespace names.
func namesN(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("ns-%d", i)
	}
	return out
}

var _ = Describe("CertReport CRD validation", func() {
	Describe("accepted objects", func() {
		It("accepts the sample", func() {
			b, err := os.ReadFile(samplePath)
			Expect(err).NotTo(HaveOccurred())
			cr := &cyclopsv1alpha1.CertReport{}
			Expect(yaml.Unmarshal(b, cr)).To(Succeed())

			Expect(k8sClient.Create(ctx, cr, client.DryRunAll)).To(Succeed())
		})

		It("fills in the defaults for a spec with only notifiers", func() {
			cr := validCertReport()

			Expect(k8sClient.Create(ctx, cr, client.DryRunAll)).To(Succeed())

			// Create decodes the server's response into cr, so these are the defaulted values.
			Expect(cr.Spec.Schedule).To(Equal("0 8 * * *"))
			Expect(cr.Spec.Suspend).To(HaveValue(BeFalse()))
			Expect(cr.Spec.StartingDeadlineSeconds).To(HaveValue(BeEquivalentTo(3600)))
			Expect(cr.Spec.SuccessfulJobsHistoryLimit).To(HaveValue(BeEquivalentTo(3)))
			Expect(cr.Spec.FailedJobsHistoryLimit).To(HaveValue(BeEquivalentTo(1)))
			Expect(cr.Spec.BackoffLimit).To(HaveValue(BeEquivalentTo(0)))
			Expect(cr.Spec.Namespaces).To(BeEmpty(), "omitted namespaces means all, so no default is added")
		})

		DescribeTable("accepts valid variations",
			func(mutate func(*cyclopsv1alpha1.CertReport)) {
				cr := validCertReport()
				mutate(cr)
				Expect(k8sClient.Create(ctx, cr, client.DryRunAll)).To(Succeed())
			},
			Entry("the @daily macro schedule", func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Schedule = "@daily" }),
			Entry("a five-field schedule with ranges and steps", func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Schedule = "*/15 8-18 * * 1-5" }),
			Entry("suspend set", func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Suspend = new(true) }),
			Entry("a namespace list", func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = []string{testNS, "staging", "a-1"} }),
			Entry("exactly 50 namespaces", func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = namesN(50) }),
			Entry("a 63-character namespace", func(cr *cyclopsv1alpha1.CertReport) {
				cr.Spec.Namespaces = []string{strings.Repeat("a", 63)}
			}),
			Entry("a template reference", func(cr *cyclopsv1alpha1.CertReport) {
				cr.Spec.EmailTemplateConfigMapRef = &cyclopsv1alpha1.TemplateConfigMapRef{Name: "cert-report-template", Key: "report.html.tmpl"}
			}),
			Entry("several Email notifiers", func(cr *cyclopsv1alpha1.CertReport) {
				cr.Spec.Notifiers = append(cr.Spec.Notifiers, cr.Spec.Notifiers[0])
			}),
			// Address content is checked by parse() when the report runs (ADR 0018 Q5), not by the CRD.
			Entry("an empty recipient list (parse rejects it at run time)", func(cr *cyclopsv1alpha1.CertReport) {
				cr.Spec.Notifiers[0].Email.To = []string{}
			}),
			Entry("a display name in from (parse rejects it at run time)", func(cr *cyclopsv1alpha1.CertReport) {
				cr.Spec.Notifiers[0].Email.From = "Cyclops <cyclops@example.com>"
			}),
		)
	})

	Describe("rejected objects", func() {
		// Each entry breaks one thing and names the field path the error must mention, so
		// the object is rejected for the intended reason and not some other mistake.
		DescribeTable("rejects",
			func(mutate func(*cyclopsv1alpha1.CertReport), wantField string) {
				cr := validCertReport()
				mutate(cr)

				err := k8sClient.Create(ctx, cr, client.DryRunAll)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(wantField))
			},
			Entry("no notifiers",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Notifiers = nil }, "spec.notifiers"),
			Entry("an empty notifier list",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Notifiers = []cyclopsv1alpha1.Notifier{} }, "spec.notifiers"),
			Entry("type Email without an email block (the CEL rule)",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Notifiers[0].Email = nil }, "spec.notifiers[0]"),
			Entry("an unknown notifier type",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Notifiers[0].Type = "Slack" }, "spec.notifiers[0].type"),
			Entry("an empty from",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Notifiers[0].Email.From = "" }, "spec.notifiers[0].email.from"),
			Entry("an omitted to",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Notifiers[0].Email.To = nil }, "spec.notifiers[0].email.to"),

			Entry("an uppercase namespace",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = []string{"Prod"} }, "spec.namespaces[0]"),
			Entry("a namespace with an underscore",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = []string{"my_ns"} }, "spec.namespaces[0]"),
			Entry("a namespace starting with a dash",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = []string{"-prod"} }, "spec.namespaces[0]"),
			Entry("a 64-character namespace",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = []string{strings.Repeat("a", 64)} }, "spec.namespaces[0]"),
			Entry("duplicate namespaces (listType=set)",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = []string{testNS, testNS} }, "spec.namespaces[1]"),
			Entry("51 namespaces",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Namespaces = namesN(51) }, "spec.namespaces"),

			Entry("a schedule that is plain text",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Schedule = "every day at 8" }, "spec.schedule"),
			Entry("a four-field schedule",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Schedule = "0 8 * *" }, "spec.schedule"),
			Entry("a six-field schedule",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Schedule = "0 0 8 * * *" }, "spec.schedule"),
			Entry("an unknown @ macro",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.Schedule = "@sometimes" }, "spec.schedule"),

			Entry("a negative backoffLimit",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.BackoffLimit = new(int32(-1)) }, "spec.backoffLimit"),
			Entry("a negative successfulJobsHistoryLimit",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.SuccessfulJobsHistoryLimit = new(int32(-1)) }, "spec.successfulJobsHistoryLimit"),
			Entry("a negative failedJobsHistoryLimit",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.FailedJobsHistoryLimit = new(int32(-1)) }, "spec.failedJobsHistoryLimit"),
			Entry("a startingDeadlineSeconds of 0",
				func(cr *cyclopsv1alpha1.CertReport) { cr.Spec.StartingDeadlineSeconds = new(int64(0)) }, "spec.startingDeadlineSeconds"),

			Entry("a template reference with an empty name",
				func(cr *cyclopsv1alpha1.CertReport) {
					cr.Spec.EmailTemplateConfigMapRef = &cyclopsv1alpha1.TemplateConfigMapRef{Name: "", Key: "report.html.tmpl"}
				}, "spec.emailTemplateConfigMapRef.name"),
			Entry("a template reference with an empty key",
				func(cr *cyclopsv1alpha1.CertReport) {
					cr.Spec.EmailTemplateConfigMapRef = &cyclopsv1alpha1.TemplateConfigMapRef{Name: "cert-report-template", Key: ""}
				}, "spec.emailTemplateConfigMapRef.key"),
		)
	})
})
