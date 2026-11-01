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

// These tests cover the Slack notifier's wiring (ADR 0017): the URL Secrets are checked into
// CredentialsFound, injected into the report Pod as env vars, watched, and part of the welcome hash.
var _ = Describe("CertReport Slack notifier", func() {
	const (
		reportName     = "slack-test"
		oncallSecret   = "slack-oncall"
		platformSecret = "slack-platform"
		mailSecret     = "slack-test-mail"
		namespace      = cyclopsNamespace
	)
	reportKey := types.NamespacedName{Name: reportName}

	slack := cyclopsv1alpha1.Notifier{Type: cyclopsv1alpha1.NotifierTypeSlack, Slack: &cyclopsv1alpha1.SlackNotifier{
		Targets: []cyclopsv1alpha1.SlackTarget{
			{Name: "oncall", URLSecretRef: cyclopsv1alpha1.SecretRef{Name: oncallSecret}},
			{Name: "platform-certs", URLSecretRef: cyclopsv1alpha1.SecretRef{Name: platformSecret}},
		},
	}}

	reconciler := func(credentialsSecret string) *CertReportReconciler {
		return &CertReportReconciler{
			Client:               k8sClient,
			Scheme:               k8sClient.Scheme(),
			Namespace:            namespace,
			Image:                testImage,
			ReportServiceAccount: testReportSA,
			CredentialsSecret:    credentialsSecret,
		}
	}
	reconcileOnce := func(r *CertReportReconciler) {
		GinkgoHelper()
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).NotTo(HaveOccurred())
	}
	credentialsCond := func() *metav1.Condition {
		GinkgoHelper()
		var cr cyclopsv1alpha1.CertReport
		Expect(k8sClient.Get(ctx, reportKey, &cr)).To(Succeed())
		return meta.FindStatusCondition(cr.Status.Conditions, cyclopsv1alpha1.ConditionCredentialsFound)
	}
	putSecret := func(name string, data map[string][]byte) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Data:       data,
		})).To(Succeed())
	}
	createReport := func(notifiers ...cyclopsv1alpha1.Notifier) *cyclopsv1alpha1.CertReport {
		GinkgoHelper()
		cr := &cyclopsv1alpha1.CertReport{
			ObjectMeta: metav1.ObjectMeta{Name: reportName},
			Spec:       cyclopsv1alpha1.CertReportSpec{Notifiers: notifiers},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		return cr
	}
	url := map[string][]byte{"url": []byte("https://hooks.example.com/x")}

	AfterEach(func() {
		for _, obj := range []client.Object{
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: reportName}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: oncallSecret, Namespace: namespace}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platformSecret, Namespace: namespace}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: mailSecret, Namespace: namespace}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: cronJobName(reportName), Namespace: namespace}},
		} {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
	})

	It("reports a missing Secret, then a missing url key, naming the target, then clears", func() {
		createReport(slack)
		r := reconciler("")
		putSecret(oncallSecret, url)

		reconcileOnce(r)
		cond := credentialsCond()
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonNotFound))
		Expect(cond.Message).To(ContainSubstring(`Slack target "platform-certs"`))

		putSecret(platformSecret, map[string][]byte{"webhook": []byte("x")})
		reconcileOnce(r)
		cond = credentialsCond()
		Expect(cond.Reason).To(Equal(reasonKeyMissing))
		Expect(cond.Message).To(ContainSubstring(`"url"`))

		var s corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platformSecret, Namespace: namespace}, &s)).To(Succeed())
		s.Data = url
		Expect(k8sClient.Update(ctx, &s)).To(Succeed())
		reconcileOnce(r)
		Expect(credentialsCond().Status).To(Equal(metav1.ConditionTrue))
	})

	It("does not check the mail Secret for a CertReport with no Email notifier", func() {
		createReport(slack)
		putSecret(oncallSecret, url)
		putSecret(platformSecret, url)

		reconcileOnce(reconciler(mailSecret)) // configured, but does not exist
		Expect(credentialsCond().Status).To(Equal(metav1.ConditionTrue))
	})

	It("checks both the mail Secret and the Slack Secrets when a CertReport has both", func() {
		createReport(slack, cyclopsv1alpha1.Notifier{
			Type:  cyclopsv1alpha1.NotifierTypeEmail,
			Email: &cyclopsv1alpha1.EmailNotifier{From: fromAddr, To: []string{testRecipient}},
		})
		putSecret(oncallSecret, url)
		putSecret(platformSecret, url)
		r := reconciler(mailSecret)

		reconcileOnce(r)
		Expect(credentialsCond().Message).To(ContainSubstring(mailSecret))

		putSecret(mailSecret, map[string][]byte{secretKeySMTPUsername: []byte("u"), secretKeySMTPPassword: []byte("p")})
		reconcileOnce(r)
		cond := credentialsCond()
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("2 Slack targets"))
	})

	It("wires each target's url into the report Pod under its CYCLOPS_SLACK_ variable, optional", func() {
		createReport(slack)
		reconcileOnce(reconciler(""))

		var cj batchv1.CronJob
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cronJobName(reportName), Namespace: namespace}, &cj)).To(Succeed())
		refs := map[string]corev1.SecretKeySelector{}
		for _, e := range cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env {
			refs[e.Name] = *e.ValueFrom.SecretKeyRef
		}
		Expect(refs).To(HaveLen(2))
		for env, secret := range map[string]string{
			"CYCLOPS_SLACK_ONCALL":         oncallSecret,
			"CYCLOPS_SLACK_PLATFORM_CERTS": platformSecret,
		} {
			Expect(refs).To(HaveKey(env))
			Expect(refs[env].Name).To(Equal(secret))
			Expect(refs[env].Key).To(Equal("url"))
			Expect(refs[env].Optional).To(HaveValue(BeTrue()))
		}
	})

	It("enqueues the CertReports that name a changed Secret, and no others", func() {
		createReport(slack)
		r := reconciler("")
		secretNamed := func(name string) client.Object {
			return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
		}
		Expect(r.certReportsForSecret(ctx, secretNamed(platformSecret))).To(ContainElement(reconcile.Request{NamespacedName: reportKey}))
		Expect(r.certReportsForSecret(ctx, secretNamed("unrelated"))).NotTo(ContainElement(reconcile.Request{NamespacedName: reportKey}))
	})

	It("changes the welcome hash when a target's Secret changes, so a failed welcome is retried after a fix", func() {
		cr := createReport(slack)
		putSecret(oncallSecret, url)
		putSecret(platformSecret, url)
		r := reconciler("")

		before, err := r.credentialsVersion(ctx, cr)
		Expect(err).NotTo(HaveOccurred())

		var s corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: oncallSecret, Namespace: namespace}, &s)).To(Succeed())
		s.Data = map[string][]byte{"url": []byte("https://hooks.example.com/rotated")}
		Expect(k8sClient.Update(ctx, &s)).To(Succeed())

		after, err := r.credentialsVersion(ctx, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(welcomeHash(cr, after)).NotTo(Equal(welcomeHash(cr, before)))
	})

	It("keeps an Email-only CertReport's welcome hash input as the mail Secret's resourceVersion alone", func() {
		cr := createReport(cyclopsv1alpha1.Notifier{
			Type:  cyclopsv1alpha1.NotifierTypeEmail,
			Email: &cyclopsv1alpha1.EmailNotifier{From: fromAddr, To: []string{testRecipient}},
		})
		putSecret(mailSecret, map[string][]byte{secretKeySMTPUsername: []byte("u"), secretKeySMTPPassword: []byte("p")})
		var s corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mailSecret, Namespace: namespace}, &s)).To(Succeed())

		v, err := reconciler(mailSecret).credentialsVersion(ctx, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(Equal(s.ResourceVersion), "an upgrade must not re-send the welcome of existing CertReports")
	})
})
