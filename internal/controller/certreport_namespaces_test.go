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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

const (
	reasonNamespaceNotFound = "NamespaceNotFound"
	reasonAllNamespaces     = "AllNamespaces"
)

var _ = Describe("CertReport namespaces", func() {
	const (
		reportName    = "ns-test"
		allReportName = "ns-test-all"
		present       = "nsfound-present"
		otherPresent  = "nsfound-other"
	)

	var (
		spy       *eventSpy
		reportKey = types.NamespacedName{Name: reportName}
	)

	reconciler := func() *CertReportReconciler {
		return &CertReportReconciler{
			Client:               k8sClient,
			Scheme:               k8sClient.Scheme(),
			Recorder:             spy,
			Namespace:            cyclopsNamespace,
			Image:                testImage,
			ReportServiceAccount: testReportSA,
		}
	}
	reconcileOnce := func() {
		GinkgoHelper()
		_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).NotTo(HaveOccurred())
	}
	createReport := func(name string, namespaces ...string) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, &cyclopsv1alpha1.CertReport{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: cyclopsv1alpha1.CertReportSpec{
				Namespaces: namespaces,
				Notifiers: []cyclopsv1alpha1.Notifier{{
					Type:  cyclopsv1alpha1.NotifierTypeEmail,
					Email: &cyclopsv1alpha1.EmailNotifier{From: fromAddr, To: []string{testRecipient}},
				}},
			},
		})).To(Succeed())
	}
	namespacesCond := func() *metav1.Condition {
		GinkgoHelper()
		var cr cyclopsv1alpha1.CertReport
		Expect(k8sClient.Get(ctx, reportKey, &cr)).To(Succeed())
		return meta.FindStatusCondition(cr.Status.Conditions, cyclopsv1alpha1.ConditionNamespacesFound)
	}
	setNamespaces := func(namespaces ...string) {
		GinkgoHelper()
		var cr cyclopsv1alpha1.CertReport
		Expect(k8sClient.Get(ctx, reportKey, &cr)).To(Succeed())
		cr.Spec.Namespaces = namespaces
		Expect(k8sClient.Update(ctx, &cr)).To(Succeed())
	}

	BeforeEach(func() {
		spy = &eventSpy{}
		// Namespaces cannot be fully deleted under envtest (there is no namespace controller),
		// so they are created once and left in place.
		for _, name := range []string{present, otherPresent} {
			err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
			Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
		}
	})

	AfterEach(func() {
		for _, obj := range []client.Object{
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: reportName}},
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: allReportName}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: cronJobName(reportName), Namespace: cyclopsNamespace}},
		} {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
	})

	It("is True with reason AllNamespaces when spec.namespaces is empty", func() {
		createReport(reportName)
		reconcileOnce()

		cond := namespacesCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonAllNamespaces))
		Expect(spy.reasons).To(BeEmpty())
	})

	It("is True with reason AnnotationMode in ingress annotation mode", func() {
		Expect(k8sClient.Create(ctx, &cyclopsv1alpha1.CertReport{
			ObjectMeta: metav1.ObjectMeta{Name: reportName},
			Spec: cyclopsv1alpha1.CertReportSpec{
				IngressAnnotationMode: true,
				Notifiers: []cyclopsv1alpha1.Notifier{{
					Type:  cyclopsv1alpha1.NotifierTypeEmail,
					Email: &cyclopsv1alpha1.EmailNotifier{From: fromAddr, To: []string{testRecipient}},
				}},
			},
		})).To(Succeed())
		reconcileOnce()

		cond := namespacesCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("AnnotationMode"))
	})

	It("is True when every listed namespace exists", func() {
		createReport(reportName, present, otherPresent)
		reconcileOnce()

		cond := namespacesCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonFound))
		Expect(spy.reasons).To(BeEmpty())
	})

	It("lists the missing namespaces sorted, warns once per change, and clears when fixed", func() {
		createReport(reportName, present, "zulu-missing", "alpha-missing")
		reconcileOnce()

		cond := namespacesCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonNamespaceNotFound))
		Expect(cond.Message).To(Equal("namespaces not found: alpha-missing, zulu-missing"))
		Expect(spy.reasons).To(Equal([]string{reasonNamespaceNotFound}))

		By("not repeating the Event while nothing changed")
		reconcileOnce()
		Expect(spy.reasons).To(HaveLen(1))

		By("warning again when the set of missing namespaces changes")
		setNamespaces(present, "alpha-missing")
		reconcileOnce()
		Expect(namespacesCond().Message).To(Equal("namespaces not found: alpha-missing"))
		Expect(spy.reasons).To(HaveLen(2))

		By("clearing the condition once every namespace exists")
		setNamespaces(present)
		reconcileOnce()
		cond = namespacesCond()
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonFound))
		Expect(spy.reasons).To(HaveLen(2))
	})

	Describe("mapping Namespace changes to CertReports", func() {
		namespaceObj := func(name string) client.Object {
			return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		}

		It("enqueues only the CertReports that name the namespace", func() {
			createReport(reportName, present)
			createReport(allReportName) // empty list: covers everything, nothing to re-check

			reqs := reconciler().certReportsForNamespace(ctx, namespaceObj(present))
			Expect(reqs).To(ConsistOf(reconcile.Request{NamespacedName: reportKey}))
		})

		It("enqueues nothing for a namespace no CertReport names", func() {
			createReport(reportName, present)

			Expect(reconciler().certReportsForNamespace(ctx, namespaceObj("unrelated"))).To(BeEmpty())
		})
	})
})
