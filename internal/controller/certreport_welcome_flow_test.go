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
	"strings"

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

// The one-time welcome email (ADR 0015). envtest has no Job controller, so the tests play its
// part by writing the Job's terminal status by hand.
var _ = Describe("CertReport welcome email", func() {
	const (
		reportName = "welcome-test"
		namespace  = cyclopsNamespace
		sentAtKey  = "welcomeSentAt"
	)

	var (
		spy       *eventSpy
		reportKey = types.NamespacedName{Name: reportName}
		stateKey  = types.NamespacedName{Name: stateConfigMapName(reportName), Namespace: namespace}
	)

	reconcileOnce := func() {
		GinkgoHelper()
		r := &CertReportReconciler{
			Client:               k8sClient,
			Scheme:               k8sClient.Scheme(),
			Recorder:             spy,
			Namespace:            namespace,
			Image:                testImage,
			ReportServiceAccount: testReportSA,
		}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).NotTo(HaveOccurred())
	}
	welcomeCond := func() *metav1.Condition {
		GinkgoHelper()
		var cr cyclopsv1alpha1.CertReport
		Expect(k8sClient.Get(ctx, reportKey, &cr)).To(Succeed())
		return meta.FindStatusCondition(cr.Status.Conditions, cyclopsv1alpha1.ConditionWelcomeSent)
	}
	// welcomeJobs lists this report's welcome Jobs (other tests leave their own behind, since
	// envtest has no garbage collector).
	welcomeJobs := func() []batchv1.Job {
		GinkgoHelper()
		var all batchv1.JobList
		Expect(k8sClient.List(ctx, &all, client.InNamespace(namespace))).To(Succeed())
		var mine []batchv1.Job
		for _, j := range all.Items {
			if strings.HasPrefix(j.Name, "cyclops-welcome-"+reportName+"-") {
				mine = append(mine, j)
			}
		}
		return mine
	}
	// deleteJob deletes with background propagation: a Job's default is to orphan its Pods, which
	// leaves it behind a finalizer that only the (absent) garbage collector would remove.
	deleteJob := func(name string) {
		GinkgoHelper()
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job,
			client.PropagationPolicy(metav1.DeletePropagationBackground)))).To(Succeed())
	}
	onlyJob := func() batchv1.Job {
		GinkgoHelper()
		jobs := welcomeJobs()
		Expect(jobs).To(HaveLen(1))
		return jobs[0]
	}
	// finish plays the Job controller: it writes the status of a Job that has run to the end.
	finish := func(job batchv1.Job, succeeded bool) {
		GinkgoHelper()
		now := metav1.Now()
		job.Status.StartTime = &now
		if succeeded {
			job.Status.Succeeded = 1
			job.Status.CompletionTime = &now
			job.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobSuccessCriteriaMet, Status: corev1.ConditionTrue, LastTransitionTime: now},
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: now},
			}
		} else {
			job.Status.Failed = 1
			job.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobFailureTarget, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", LastTransitionTime: now},
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", LastTransitionTime: now},
			}
		}
		Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())
	}

	BeforeEach(func() {
		spy = &eventSpy{}
		Expect(k8sClient.Create(ctx, &cyclopsv1alpha1.CertReport{
			ObjectMeta: metav1.ObjectMeta{Name: reportName},
			Spec: cyclopsv1alpha1.CertReportSpec{
				Notifiers: []cyclopsv1alpha1.Notifier{{
					Type:  cyclopsv1alpha1.NotifierTypeEmail,
					Email: &cyclopsv1alpha1.EmailNotifier{From: fromAddr, To: []string{testRecipient}},
				}},
			},
		})).To(Succeed())
	})

	AfterEach(func() {
		objs := []client.Object{
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: reportName}},
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: stateKey.Name, Namespace: namespace}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: cronJobName(reportName), Namespace: namespace}},
		}
		for _, obj := range objs {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
		for _, j := range welcomeJobs() {
			deleteJob(j.Name)
		}
	})

	It("creates a Job that runs the report with --welcome, owned by the CertReport", func() {
		reconcileOnce()

		job := onlyJob()
		args := job.Spec.Template.Spec.Containers[0].Args
		Expect(args).To(Equal([]string{
			"report", "--certreport=" + reportName, "--namespace=" + namespace, "--welcome",
		}))
		Expect(job.Spec.TTLSecondsAfterFinished).NotTo(BeNil())
		Expect(job.OwnerReferences).To(HaveLen(1))
		Expect(job.OwnerReferences[0].Name).To(Equal(reportName))

		By("leaving the CronJob's own args without --welcome")
		var cj batchv1.CronJob
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cronJobName(reportName), Namespace: namespace}, &cj)).To(Succeed())
		Expect(cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args).NotTo(ContainElement("--welcome"))

		cond := welcomeCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal("Pending"))
	})

	It("does not create a second Job on the next reconcile, and reports it as running", func() {
		reconcileOnce()
		reconcileOnce()

		Expect(welcomeJobs()).To(HaveLen(1))
		cond := welcomeCond()
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal("Running"))
	})

	It("records welcomeSentAt and sets WelcomeSent=True when the Job succeeds", func() {
		reconcileOnce()
		finish(onlyJob(), true)
		reconcileOnce()

		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, stateKey, &cm)).To(Succeed())
		Expect(cm.Data).To(HaveKey(sentAtKey))
		Expect(cm.OwnerReferences).To(HaveLen(1))
		Expect(cm.OwnerReferences[0].Name).To(Equal(reportName))

		cond := welcomeCond()
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(spy.reasons).To(BeEmpty())

		By("not sending again once the Job is gone")
		deleteJob(onlyJob().Name)
		reconcileOnce()
		Expect(welcomeJobs()).To(BeEmpty())
		Expect(welcomeCond().Status).To(Equal(metav1.ConditionTrue))
	})

	It("keeps the first welcomeSentAt if it runs again", func() {
		reconcileOnce()
		finish(onlyJob(), true)
		reconcileOnce()

		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, stateKey, &cm)).To(Succeed())
		first := cm.Data[sentAtKey]

		reconcileOnce()
		Expect(k8sClient.Get(ctx, stateKey, &cm)).To(Succeed())
		Expect(cm.Data[sentAtKey]).To(Equal(first))
	})

	It("creates no Job when welcomeSentAt is already recorded", func() {
		Expect(k8sClient.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: stateKey.Name, Namespace: namespace},
			Data:       map[string]string{sentAtKey: "2026-10-25T08:00:00Z"},
		})).To(Succeed())

		reconcileOnce()

		Expect(welcomeJobs()).To(BeEmpty())
		cond := welcomeCond()
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("2026-10-25T08:00:00Z"))
	})

	It("sets WelcomeSent=False and warns when the Job fails, without recording anything", func() {
		reconcileOnce()
		finish(onlyJob(), false)
		reconcileOnce()

		cond := welcomeCond()
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("JobFailed"))
		Expect(spy.reasons).To(ConsistOf("JobFailed"))

		var cm corev1.ConfigMap
		err := k8sClient.Get(ctx, stateKey, &cm)
		Expect(client.IgnoreNotFound(err)).To(Succeed())
		Expect(cm.Data).NotTo(HaveKey(sentAtKey))

		By("warning only once for the same failure")
		reconcileOnce()
		Expect(spy.reasons).To(HaveLen(1))
	})

	It("does not retry a failed Job whose TTL has deleted it, but does after the CertReport changes", func() {
		reconcileOnce()
		failed := onlyJob()
		finish(failed, false)
		reconcileOnce()

		By("the TTL removing the failed Job")
		deleteJob(failed.Name)
		reconcileOnce()
		Expect(welcomeJobs()).To(BeEmpty())
		Expect(welcomeCond().Reason).To(Equal("JobFailed"))

		By("editing the CertReport, which changes the hash")
		var cr cyclopsv1alpha1.CertReport
		Expect(k8sClient.Get(ctx, reportKey, &cr)).To(Succeed())
		cr.Spec.Schedule = "15 5 * * *"
		Expect(k8sClient.Update(ctx, &cr)).To(Succeed())
		reconcileOnce()

		retry := onlyJob()
		Expect(retry.Name).NotTo(Equal(failed.Name))
		Expect(welcomeCond().Reason).To(Equal("Pending"))
	})

	It("waits, and creates no Job, while the credentials Secret is missing", func() {
		r := &CertReportReconciler{
			Client:               k8sClient,
			Scheme:               k8sClient.Scheme(),
			Recorder:             spy,
			Namespace:            namespace,
			Image:                testImage,
			ReportServiceAccount: testReportSA,
			CredentialsSecret:    "no-such-secret",
		}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).NotTo(HaveOccurred())

		Expect(welcomeJobs()).To(BeEmpty())
		cond := welcomeCond()
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal("WaitingForCredentials"))
	})
})
