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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
)

// eventSpy records the reasons of the Events a reconciler emits.
type eventSpy struct{ reasons []string }

func (s *eventSpy) Eventf(_, _ runtime.Object, _, reason, _, _ string, _ ...any) {
	s.reasons = append(s.reasons, reason)
}

func (s *eventSpy) AnnotatedEventf(regarding, related runtime.Object, _ map[string]string,
	eventType, reason, action, note string, args ...any) {
	s.Eventf(regarding, related, eventType, reason, action, note, args...)
}

// Values shared by the controller tests.
const (
	testImage     = "cyclops:test"
	testReportSA  = "cyclops-report"
	testRecipient = "platform@example.com"
	fromAddr      = "cyclops@example.com"
	// cyclopsNamespace is where the reconciler puts the CronJob and finds the Secret.
	cyclopsNamespace = "default"
)

const (
	reasonFound      = "Found"
	reasonNotFound   = "SecretNotFound"
	reasonKeyMissing = "SecretKeyMissing"
)

var _ = Describe("CertReport credentials", func() {
	const (
		reportName = "creds-test"
		secretName = "mail-credentials"
		namespace  = cyclopsNamespace
	)

	var (
		spy       *eventSpy
		reportKey = types.NamespacedName{Name: reportName}
	)

	reconciler := func(credentialsSecret string) *CertReportReconciler {
		return &CertReportReconciler{
			Client:               k8sClient,
			Scheme:               k8sClient.Scheme(),
			Recorder:             spy,
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
	putSecret := func(data map[string][]byte) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			Data:       data,
		})).To(Succeed())
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
		for _, obj := range []client.Object{
			&cyclopsv1alpha1.CertReport{ObjectMeta: metav1.ObjectMeta{Name: reportName}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: cronJobName(reportName), Namespace: namespace}},
		} {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
	})

	It("is True with reason NotConfigured when no Secret is configured", func() {
		reconcileOnce(reconciler(""))

		cond := credentialsCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("NotConfigured"))
		Expect(spy.reasons).To(BeEmpty())
	})

	It("is False with reason SecretNotFound and warns once, then clears when the Secret appears", func() {
		r := reconciler(secretName)
		reconcileOnce(r)

		cond := credentialsCond()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonNotFound))
		Expect(cond.Message).To(ContainSubstring(secretName))
		Expect(spy.reasons).To(Equal([]string{reasonNotFound}))

		By("not repeating the Event while the condition stays the same")
		reconcileOnce(r)
		Expect(spy.reasons).To(HaveLen(1))

		By("clearing the condition once the Secret exists")
		putSecret(map[string][]byte{secretKeySMTPUsername: []byte("u"), secretKeySMTPPassword: []byte("p")})
		reconcileOnce(r)
		cond = credentialsCond()
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonFound))
		Expect(spy.reasons).To(HaveLen(1))
	})

	DescribeTable("checks the keys in the Secret",
		func(data map[string][]byte, wantStatus metav1.ConditionStatus, wantReason string) {
			putSecret(data)
			reconcileOnce(reconciler(secretName))

			cond := credentialsCond()
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(wantStatus))
			Expect(cond.Reason).To(Equal(wantReason))
			if wantStatus == metav1.ConditionFalse {
				Expect(spy.reasons).To(Equal([]string{wantReason}))
			}
		},
		Entry("SMTP pair",
			map[string][]byte{secretKeySMTPUsername: []byte("u"), secretKeySMTPPassword: []byte("p")},
			metav1.ConditionTrue, reasonFound),
		Entry("SES pair",
			map[string][]byte{secretKeySESAccessKey: []byte("a"), secretKeySESSecretKey: []byte("s")},
			metav1.ConditionTrue, reasonFound),
		Entry("only half of the SMTP pair",
			map[string][]byte{secretKeySMTPUsername: []byte("u")},
			metav1.ConditionFalse, reasonKeyMissing),
		Entry("an empty value counts as missing",
			map[string][]byte{secretKeySMTPUsername: []byte("u"), secretKeySMTPPassword: {}},
			metav1.ConditionFalse, reasonKeyMissing),
		Entry("one key from each pair",
			map[string][]byte{secretKeySMTPUsername: []byte("u"), secretKeySESSecretKey: []byte("s")},
			metav1.ConditionFalse, reasonKeyMissing),
		Entry("unrelated keys only",
			map[string][]byte{"token": []byte("t")},
			metav1.ConditionFalse, reasonKeyMissing),
	)

	It("wires every credentials key into the report Pod as an optional env var", func() {
		reconcileOnce(reconciler(secretName))

		var cj batchv1.CronJob
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cronJobName(reportName), Namespace: namespace}, &cj)).To(Succeed())
		env := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env
		keys := map[string]string{}
		for _, e := range env {
			ref := e.ValueFrom.SecretKeyRef
			Expect(ref.Name).To(Equal(secretName))
			Expect(ref.Optional).To(HaveValue(BeTrue()))
			keys[e.Name] = ref.Key
		}
		Expect(keys).To(Equal(map[string]string{
			"CYCLOPS_SMTP_USERNAME": "username",
			"CYCLOPS_SMTP_PASSWORD": "password",
			"AWS_ACCESS_KEY_ID":     "access-key-id",
			"AWS_SECRET_ACCESS_KEY": "secret-access-key",
		}))
	})

	Describe("mapping Secret changes to CertReports", func() {
		secretNamed := func(ns, name string) client.Object {
			return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		}

		It("enqueues every CertReport for the credentials Secret", func() {
			reqs := reconciler(secretName).certReportsForSecret(context.Background(), secretNamed(namespace, secretName))
			Expect(reqs).To(ContainElement(reconcile.Request{NamespacedName: reportKey}))
		})

		DescribeTable("ignores other Secrets",
			func(configured, ns, name string) {
				Expect(reconciler(configured).certReportsForSecret(ctx, secretNamed(ns, name))).To(BeEmpty())
			},
			Entry("another name", secretName, namespace, "unrelated"),
			Entry("another namespace", secretName, "kube-system", secretName),
			Entry("no Secret configured", "", namespace, secretName),
		)
	})

	It("treats a Secret read error other than NotFound as a reconcile error", func() {
		failing := reconciler(secretName)
		failing.Client = erroringSecretReader{Client: k8sClient}

		_, err := failing.Reconcile(ctx, reconcile.Request{NamespacedName: reportKey})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeFalse())
		Expect(err.Error()).To(ContainSubstring("reading credentials Secret"))
	})
})

// erroringSecretReader fails every Secret read with a non-NotFound error.
type erroringSecretReader struct{ client.Client }

func (e erroringSecretReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Secret); ok {
		return fmt.Errorf("connection refused")
	}
	return e.Client.Get(ctx, key, obj, opts...)
}
