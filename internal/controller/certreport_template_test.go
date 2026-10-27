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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

const (
	reasonBuiltIn          = "BuiltIn"
	reasonValid            = "Valid"
	reasonConfigMapMissing = "ConfigMapNotFound"
	reasonTemplateKey      = "KeyMissing"
	reasonParseError       = "ParseError"

	// goodTemplate parses and only uses fields that exist.
	goodTemplate = "<p>{{ .ReportName }}</p>"
)

var _ = Describe("CertReport template", func() {
	const (
		reportName    = "tmpl-test"
		otherName     = "tmpl-test-other"
		configMapName = "cert-report-template"
		defaultName   = "cyclops-default-template"
		key           = "report.html"
		namespace     = cyclopsNamespace
	)

	var (
		spy       *eventSpy
		reportKey = types.NamespacedName{Name: reportName}
	)

	reconciler := func(defaultConfigMap string) *CertReportReconciler {
		return &CertReportReconciler{
			Client:                   k8sClient,
			Scheme:                   k8sClient.Scheme(),
			Recorder:                 spy,
			Namespace:                namespace,
			Image:                    testImage,
			ReportServiceAccount:     testReportSA,
			DefaultTemplateConfigMap: defaultConfigMap,
		}
	}
	reconcileOnce := func(r *CertReportReconciler) {
		GinkgoHelper()
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).NotTo(HaveOccurred())
	}
	templateCond := func() *metav1.Condition {
		GinkgoHelper()
		var cr cyclopsv1alpha1.CertReport
		Expect(k8sClient.Get(ctx, reportKey, &cr)).To(Succeed())
		return meta.FindStatusCondition(cr.Status.Conditions, cyclopsv1alpha1.ConditionTemplateValid)
	}
	createReport := func(ref *cyclopsv1alpha1.TemplateConfigMapRef) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, templateReport(reportName, ref))).To(Succeed())
	}
	createOther := func(ref *cyclopsv1alpha1.TemplateConfigMapRef) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, templateReport(otherName, ref))).To(Succeed())
	}
	ownRef := func() *cyclopsv1alpha1.TemplateConfigMapRef {
		return &cyclopsv1alpha1.TemplateConfigMapRef{Name: configMapName, Key: key}
	}
	putConfigMap := func(name string, data map[string]string) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Data:       data,
		})).To(Succeed())
	}

	AfterEach(func() {
		for _, obj := range []client.Object{
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: reportName}},
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: otherName}},
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: namespace}},
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: defaultName, Namespace: namespace}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: cronJobName(reportName), Namespace: namespace}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: cronJobName(otherName), Namespace: namespace}},
		} {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
	})

	BeforeEach(func() { spy = &eventSpy{} })

	It("is True with reason BuiltIn when no template is configured", func() {
		createReport(nil)
		reconcileOnce(reconciler(""))

		cond := templateCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonBuiltIn))
		Expect(spy.reasons).To(BeEmpty())
	})

	It("is False with reason ConfigMapNotFound and warns once, then clears when the ConfigMap appears", func() {
		createReport(ownRef())
		r := reconciler("")
		reconcileOnce(r)

		cond := templateCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonConfigMapMissing))
		Expect(cond.Message).To(ContainSubstring(configMapName))
		Expect(spy.reasons).To(Equal([]string{reasonConfigMapMissing}))

		By("not repeating the Event while the condition stays the same")
		reconcileOnce(r)
		Expect(spy.reasons).To(HaveLen(1))

		By("clearing the condition once the ConfigMap exists")
		putConfigMap(configMapName, map[string]string{key: goodTemplate})
		reconcileOnce(r)
		cond = templateCond()
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonValid))
		Expect(spy.reasons).To(HaveLen(1))
	})

	DescribeTable("checks the key and the syntax of the template",
		func(data map[string]string, wantStatus metav1.ConditionStatus, wantReason, wantMessage string) {
			createReport(ownRef())
			putConfigMap(configMapName, data)
			reconcileOnce(reconciler(""))

			cond := templateCond()
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(wantStatus))
			Expect(cond.Reason).To(Equal(wantReason))
			Expect(cond.Message).To(ContainSubstring(wantMessage))
			if wantStatus == metav1.ConditionFalse {
				Expect(spy.reasons).To(Equal([]string{wantReason}))
			}
		},
		Entry("a valid template",
			map[string]string{key: "<p>{{ range .Findings }}{{ .Name }}{{ end }}</p>"},
			metav1.ConditionTrue, reasonValid, key),
		Entry("the key is not in the ConfigMap",
			map[string]string{"other.html": "<p>hi</p>"},
			metav1.ConditionFalse, reasonTemplateKey, key),
		Entry("an unclosed action",
			map[string]string{key: "<p>{{ .ReportName"},
			metav1.ConditionFalse, reasonParseError, "unclosed action"),
		Entry("an unknown function",
			map[string]string{key: "<p>{{ nosuchfunc .ReportName }}</p>"},
			metav1.ConditionFalse, reasonParseError, "nosuchfunc"),
		// The welcome template is optional (ADR 0015 amendment): absent is fine, present must parse.
		Entry("a valid welcome template next to the report template",
			map[string]string{key: goodTemplate, "welcome.html": "<p>{{ .Schedule }}</p>"},
			metav1.ConditionTrue, reasonValid, "welcome.html"),
		Entry("a welcome template that does not parse",
			map[string]string{key: goodTemplate, "welcome.html": "<p>{{ .Schedule"},
			metav1.ConditionFalse, reasonParseError, "welcome.html"),
		// Only syntax is checked (ADR 0018): a misspelled field fails when the template runs.
		Entry("a field that does not exist is not caught",
			map[string]string{key: "<p>{{ .Findngs }}</p>"},
			metav1.ConditionTrue, reasonValid, key),
	)

	Describe("the chart-wide default template", func() {
		It("checks the default ConfigMap, key template.html, for a CertReport without its own reference", func() {
			createReport(nil)
			r := reconciler(defaultName)
			reconcileOnce(r)

			cond := templateCond()
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonConfigMapMissing))
			Expect(cond.Message).To(ContainSubstring(defaultName))

			putConfigMap(defaultName, map[string]string{"template.html": goodTemplate})
			reconcileOnce(r)
			cond = templateCond()
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonValid))
		})

		It("is ignored when the CertReport has a reference of its own", func() {
			createReport(ownRef())
			putConfigMap(configMapName, map[string]string{key: goodTemplate})
			reconcileOnce(reconciler(defaultName)) // the default ConfigMap does not exist

			Expect(templateCond().Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Describe("mapping ConfigMap changes to CertReports", func() {
		configMapNamed := func(ns, name string) client.Object {
			return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		}

		It("enqueues the CertReports that reference the ConfigMap, and no others", func() {
			createReport(ownRef())
			createOther(&cyclopsv1alpha1.TemplateConfigMapRef{Name: "unrelated", Key: key})

			reqs := reconciler("").certReportsForConfigMap(ctx, configMapNamed(namespace, configMapName))
			Expect(reqs).To(ContainElement(reconcile.Request{NamespacedName: reportKey}))
			Expect(reqs).NotTo(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: otherName}}))
		})

		It("enqueues the CertReports without a reference of their own for the default ConfigMap", func() {
			createReport(nil)
			createOther(ownRef())

			reqs := reconciler(defaultName).certReportsForConfigMap(ctx, configMapNamed(namespace, defaultName))
			Expect(reqs).To(ContainElement(reconcile.Request{NamespacedName: reportKey}))
			Expect(reqs).NotTo(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: otherName}}))
		})

		DescribeTable("ignores other ConfigMaps",
			func(defaultConfigMap, ns, name string) {
				createReport(ownRef())
				Expect(reconciler(defaultConfigMap).certReportsForConfigMap(ctx, configMapNamed(ns, name))).To(BeEmpty())
			},
			Entry("another name", "", namespace, "unrelated"),
			Entry("another namespace", "", "kube-system", configMapName),
			Entry("the default name when a reference of its own is set", defaultName, namespace, defaultName),
		)
	})

	It("treats a ConfigMap read error other than NotFound as a reconcile error", func() {
		createReport(ownRef())
		failing := reconciler("")
		failing.Client = erroringConfigMapReader{Client: k8sClient}

		_, err := failing.Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeFalse())
		Expect(err.Error()).To(ContainSubstring("reading template ConfigMap"))
	})
})

// templateReport is a minimal valid CertReport named name with the given template reference.
func templateReport(name string, ref *cyclopsv1alpha1.TemplateConfigMapRef) *cyclopsv1alpha1.CertReport {
	return &cyclopsv1alpha1.CertReport{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: cyclopsv1alpha1.CertReportSpec{
			EmailTemplateConfigMapRef: ref,
			Notifiers: []cyclopsv1alpha1.Notifier{{
				Type:  cyclopsv1alpha1.NotifierTypeEmail,
				Email: &cyclopsv1alpha1.EmailNotifier{From: fromAddr, To: []string{testRecipient}},
			}},
		},
	}
}

// erroringConfigMapReader fails every ConfigMap read with a non-NotFound error.
type erroringConfigMapReader struct{ client.Client }

func (e erroringConfigMapReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.ConfigMap); ok {
		return fmt.Errorf("connection refused")
	}
	return e.Client.Get(ctx, key, obj, opts...)
}
