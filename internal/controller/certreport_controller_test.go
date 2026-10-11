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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

var _ = Describe("CertReport Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		certreport := &cyclopsv1alpha1.CertReport{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind CertReport")
			err := k8sClient.Get(ctx, typeNamespacedName, certreport)
			if err != nil && errors.IsNotFound(err) {
				resource := &cyclopsv1alpha1.CertReport{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
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
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &cyclopsv1alpha1.CertReport{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			// envtest runs no garbage collector, so the owned CronJob is not removed with the
			// CertReport; each test deletes the CronJob it creates.
			By("Cleanup the specific resource instance CertReport")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should create an owned CronJob and mark the schedule valid", func() {
			controllerReconciler := &CertReportReconciler{
				Client:               k8sClient,
				Scheme:               k8sClient.Scheme(),
				Namespace:            cyclopsNamespace,
				Image:                testImage,
				ReportServiceAccount: testReportSA,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating the CronJob from the spec")
			cj := &batchv1.CronJob{}
			cjKey := types.NamespacedName{Name: cronJobName(resourceName), Namespace: cyclopsNamespace}
			Expect(k8sClient.Get(ctx, cjKey, cj)).To(Succeed())
			Expect(cj.Spec.Schedule).To(Equal("0 8 * * *"))
			Expect(cj.Spec.ConcurrencyPolicy).To(Equal(batchv1.ForbidConcurrent))
			pod := cj.Spec.JobTemplate.Spec.Template.Spec
			Expect(pod.ServiceAccountName).To(Equal(testReportSA))
			Expect(pod.Containers).To(HaveLen(1))
			Expect(pod.Containers[0].Image).To(Equal(testImage))
			Expect(pod.Containers[0].Args).To(Equal([]string{
				"report", "--certreport=" + resourceName, "--namespace=" + cyclopsNamespace,
			}))

			By("owning it through the CertReport")
			Expect(cj.OwnerReferences).To(HaveLen(1))
			Expect(cj.OwnerReferences[0].Name).To(Equal(resourceName))

			By("setting ScheduleValid=True")
			Expect(k8sClient.Get(ctx, typeNamespacedName, certreport)).To(Succeed())
			cond := meta.FindStatusCondition(certreport.Status.Conditions, cyclopsv1alpha1.ConditionScheduleValid)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.ObservedGeneration).To(Equal(certreport.Generation))

			Expect(k8sClient.Delete(ctx, cj)).To(Succeed())
		})

		// The controller reads from a cache that can lag behind the API server. A status write
		// based on a stale copy must still succeed instead of failing with a version conflict.
		It("writes status even when its copy of the CertReport is stale", func() {
			controllerReconciler := &CertReportReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

			var stale cyclopsv1alpha1.CertReport
			Expect(k8sClient.Get(ctx, typeNamespacedName, &stale)).To(Succeed())

			By("changing the object behind the stale copy's back")
			var current cyclopsv1alpha1.CertReport
			Expect(k8sClient.Get(ctx, typeNamespacedName, &current)).To(Succeed())
			current.Spec.Schedule = "30 6 * * *"
			Expect(k8sClient.Update(ctx, &current)).To(Succeed())
			Expect(current.ResourceVersion).NotTo(Equal(stale.ResourceVersion))

			By("writing status from the stale copy")
			Expect(controllerReconciler.updateStatus(ctx, &stale, metav1.Condition{
				Type:    cyclopsv1alpha1.ConditionScheduleValid,
				Status:  metav1.ConditionTrue,
				Reason:  "Accepted",
				Message: "test",
			})).To(Succeed())

			By("keeping the condition, and not reverting the newer spec")
			Expect(k8sClient.Get(ctx, typeNamespacedName, certreport)).To(Succeed())
			Expect(meta.FindStatusCondition(certreport.Status.Conditions, cyclopsv1alpha1.ConditionScheduleValid)).NotTo(BeNil())
			Expect(certreport.Spec.Schedule).To(Equal("30 6 * * *"))
		})

		// ADR 0018: the CRD only checks the shape of the schedule, so a five-field junk
		// value gets through and the CronJob update is rejected by the API server. The old
		// CronJob must keep running and ScheduleValid must carry the server's message.
		It("should keep the old CronJob and set ScheduleValid=False when the schedule is rejected", func() {
			controllerReconciler := &CertReportReconciler{
				Client:               k8sClient,
				Scheme:               k8sClient.Scheme(),
				Namespace:            cyclopsNamespace,
				Image:                testImage,
				ReportServiceAccount: testReportSA,
			}
			req := reconcile.Request{NamespacedName: typeNamespacedName}
			cjKey := types.NamespacedName{Name: cronJobName(resourceName), Namespace: cyclopsNamespace}

			By("reconciling a valid schedule first")
			_, err := controllerReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			By("editing the schedule to something the CRD accepts but cron does not")
			Expect(k8sClient.Get(ctx, typeNamespacedName, certreport)).To(Succeed())
			certreport.Spec.Schedule = "99 99 99 99 99"
			Expect(k8sClient.Update(ctx, certreport)).To(Succeed())

			By("reconciling without an error, since retrying cannot fix a bad spec")
			_, err = controllerReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			By("leaving the previous CronJob schedule in place")
			cj := &batchv1.CronJob{}
			Expect(k8sClient.Get(ctx, cjKey, cj)).To(Succeed())
			Expect(cj.Spec.Schedule).To(Equal("0 8 * * *"))

			By("setting ScheduleValid=False with the API server's message")
			Expect(k8sClient.Get(ctx, typeNamespacedName, certreport)).To(Succeed())
			cond := meta.FindStatusCondition(certreport.Status.Conditions, cyclopsv1alpha1.ConditionScheduleValid)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("Rejected"))
			Expect(cond.Message).To(ContainSubstring("schedule"))
			Expect(cond.ObservedGeneration).To(Equal(certreport.Generation))

			By("recovering once the schedule is fixed")
			certreport.Spec.Schedule = "30 6 * * *"
			Expect(k8sClient.Update(ctx, certreport)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, cjKey, cj)).To(Succeed())
			Expect(cj.Spec.Schedule).To(Equal("30 6 * * *"))
			Expect(k8sClient.Get(ctx, typeNamespacedName, certreport)).To(Succeed())
			cond = meta.FindStatusCondition(certreport.Status.Conditions, cyclopsv1alpha1.ConditionScheduleValid)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))

			Expect(k8sClient.Delete(ctx, cj)).To(Succeed())
		})
	})
})
