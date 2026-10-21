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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

	cyclopsv1alpha1 "github.com/sakshitposting-irl/cyclops/api/v1alpha1"
	"github.com/sakshitposting-irl/cyclops/internal/email"
)

// CertReportReconciler turns a CertReport into the Kubernetes objects that run it.
//
// It does not do the reporting itself (ADR 0006). For each CertReport it keeps an owned
// CronJob in sync with the spec, and records what it finds (schedule accepted, credentials
// present, ...) as status conditions. Kubernetes' built-in CronJob controller then fires the
// CronJob, and the Pod it starts runs this same binary in report mode.
type CertReportReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Recorder emits Events on a CertReport (e.g. the WelcomeSent failure warning).
	// kubectl describe shows them, and they are also in the controller's logs. It is nil in unit tests.
	Recorder recorder.EventRecorder

	// Namespace is the cyclops namespace. The owned CronJob, Jobs, state ConfigMap,
	// template ConfigMap and credentials Secret all live here (ADR 0014, ADR 0018 Q7).
	Namespace string

	// Image is the image the report Pod runs: the same binary as the controller,
	// started in report mode (ADR 0006).
	Image string

	// ReportServiceAccount is the ServiceAccount the report Pod runs as. It is the
	// only identity with read access to Certificates (config/rbac/report_*.yaml).
	ReportServiceAccount string

	// CredentialsSecret and MailConfigMap name the install-level Secret and ConfigMap
	// (ADR 0014) that are wired into the report Pod as env vars, so the controller
	// itself never reads the password. The variable names are in internal/email/env.go.
	CredentialsSecret string
	MailConfigMap     string

	// DefaultTemplateConfigMap, if set, is passed to the report Pod as the template for every
	// CertReport that has no emailTemplateConfigMapRef of its own. Empty means the built-in one.
	DefaultTemplateConfigMap string
}

// The report Pod runs as its own ServiceAccount with read-only access (ADR 0010). That
// RBAC is hand-written in config/rbac/report_*.yaml, because the markers below only
// generate the manager's role. The controller sets serviceAccountName on the CronJob's
// Pod template.

// +kubebuilder:rbac:groups=cyclops.sakshitposting-irl.github.io,resources=certreports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cyclops.sakshitposting-irl.github.io,resources=certreports/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cyclops.sakshitposting-irl.github.io,resources=certreports/finalizers,verbs=update

// The CronJob lives in the cyclops namespace, so the manager gets a namespaced Role for it.
// +kubebuilder:rbac:groups=batch,namespace=system,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete

// The credentials Secret is only read to check it exists and has the expected keys (ADR 0014).
// +kubebuilder:rbac:groups="",namespace=system,resources=secrets,verbs=get;list;watch

// Events about a cluster-scoped CertReport are recorded outside the cyclops namespace.
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Namespaces are cluster-scoped: read to validate spec.namespaces and watched so the
// condition follows namespaces being created or deleted (ADR 0011).
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// TODO(adr0015): RBAC markers for the rest of the cyclops namespace: jobs
// (get;list;watch;create;update;patch;delete), configmaps (get;list;watch;create;update).

// Reconcile brings the cluster in line with one CertReport. It runs whenever the CertReport,
// its CronJob or the credentials Secret changes, and is safe to run repeatedly: when nothing
// has changed it performs no writes.
//
// Each pass:
//  1. Load the CertReport (nothing to do if it is gone or being deleted).
//  2. Create or update the owned CronJob from the spec, and set ScheduleValid from the result.
//  3. Check the mail credentials Secret and set CredentialsFound.
//  4. Check that the namespaces in the spec exist and set NamespacesFound.
//  5. Write the status conditions once, and only if they changed.
//
// For the Reconcile contract, see
// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/reconcile
func (r *CertReportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// The logger travels in the context, so log lines carry this reconcile's request details.
	log := logf.FromContext(ctx)

	// The reconciler embeds client.Client, so r.Get is that client's Get. For the kinds the
	// manager caches it reads from the local cache, not the API server.
	var cr cyclopsv1alpha1.CertReport
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		// Already deleted: nothing to do, and its CronJob is garbage collected through the
		// owner reference. Any other error is returned so the request is retried.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cr.DeletionTimestamp.IsZero() {
		// Being deleted: leave the owned objects to garbage collection.
		return ctrl.Result{}, nil
	}

	// Step 2: the owned CronJob. CreateOrUpdate fetches it (or starts from this empty object),
	// runs mutateCronJob, and writes only if the result differs. mutateCronJob sets just the
	// fields we own and leaves the API server's defaults alone, so an unchanged CertReport
	// causes no update.
	cj := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{
		Name:      cronJobName(cr.Name),
		Namespace: r.Namespace,
	}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cj, func() error {
		return r.mutateCronJob(cj, &cr)
	})

	// What the API server said about the CronJob becomes the ScheduleValid condition.
	var schedule metav1.Condition
	switch {
	case err == nil:
		log.V(1).Info("CronJob reconciled", "cronJob", cj.Name, "operation", op)
		schedule = metav1.Condition{
			Type:    cyclopsv1alpha1.ConditionScheduleValid,
			Status:  metav1.ConditionTrue,
			Reason:  "Accepted",
			Message: "the CronJob was accepted by the API server",
		}
	case apierrors.IsInvalid(err):
		// ADR 0018: the API server has the final word on the schedule, because the CRD only
		// checks its shape. On a rejected edit the previous CronJob keeps running its old
		// schedule and this condition shows the mismatch. Retrying cannot help until the spec
		// changes (which triggers a new reconcile), so no error is returned.
		log.Info("CronJob rejected by the API server", "cronJob", cj.Name, "reason", err.Error())
		schedule = metav1.Condition{
			Type:    cyclopsv1alpha1.ConditionScheduleValid,
			Status:  metav1.ConditionFalse,
			Reason:  "Rejected",
			Message: err.Error(),
		}
	default:
		// Anything else (API unavailable, conflict, forbidden) may be transient: retry.
		return ctrl.Result{}, fmt.Errorf("reconciling CronJob %s/%s: %w", cj.Namespace, cj.Name, err)
	}

	// Step 3: the credentials Secret. The Event is emitted before the status write because it
	// compares the new condition with the one currently stored.
	creds, err := r.credentialsCondition(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.warnOnTransition(&cr, creds)

	// Step 4: the namespaces the report covers.
	nsCond, err := r.namespacesCondition(ctx, &cr)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.warnOnTransition(&cr, nsCond)

	// Step 4b: did the last scheduled run work? Read from the CronJob we just fetched, so no
	// extra API call and no Job watch (ADR 0019).
	report := reportCondition(cj, meta.FindStatusCondition(cr.Status.Conditions, cyclopsv1alpha1.ConditionReportSucceeding))
	r.warnOnTransition(&cr, report)

	// TODO(adr0018): TemplateValid: if emailTemplateConfigMapRef is set, load the
	// ConfigMap key from the cyclops namespace and parse it with internal/render.
	// TODO(adr0015): welcome email: if the state ConfigMap cyclops-state-<name> has no
	// welcomeSentAt, create a one-off Job from the CronJob template with --welcome
	// (name includes a hash of generation + credentials Secret resourceVersion).
	// On Job success write welcomeSentAt and set WelcomeSent=True; on failure set
	// WelcomeSent=False reason JobFailed and emit a Warning Event.
	// No finalizers are needed: the CronJob, Jobs and state ConfigMap are garbage
	// collected through their owner reference to the cluster-scoped CertReport.

	// Step 5: one status write for all conditions.
	return ctrl.Result{}, r.updateStatus(ctx, &cr, schedule, creds, nsCond, report)
}

// reportCondition turns the CronJob's own status into the ReportSucceeding condition (ADR
// 0019). prev is the condition currently stored on the CertReport, or nil. Checked in order:
//   - suspended: Unknown, "Suspended".
//   - never scheduled: Unknown, "NoRunsYet".
//   - a run is active: keep prev, so the old verdict does not flicker while it runs.
//   - last success missing or older than the last schedule: False, "LastRunFailed".
//   - otherwise: True, "LastRunSucceeded".
//
// Messages are stable text (CronJob timestamps, never time.Now()), or every reconcile would
// write status.
func reportCondition(cj *batchv1.CronJob, prev *metav1.Condition) metav1.Condition {
	cond := metav1.Condition{Type: cyclopsv1alpha1.ConditionReportSucceeding}

	if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
		cond.Status = metav1.ConditionUnknown
		cond.Reason = "Suspended"
		cond.Message = "the CronJob is suspended"
		return cond
	}

	if cj.Status.LastScheduleTime == nil {
		cond.Status = metav1.ConditionUnknown
		cond.Reason = "NoRunsYet"
		cond.Message = "no scheduled run has happened yet"
		return cond
	}

	if len(cj.Status.Active) > 0 {
		if prev != nil {
			// Keep the previous condition while a run is active, so it does not flip back and
			// forth while the run is still in progress.
			return *prev
		}
		// Mostly unreachable, kept for completeness: the NoRunsYet condition is stored before
		// the first run can become active, so prev is normally set by now.
		cond.Status = metav1.ConditionUnknown
		cond.Reason = "RunInProgress"
		cond.Message = "a scheduled run is currently active"
		return cond
	}

	if cj.Status.LastSuccessfulTime == nil || cj.Status.LastSuccessfulTime.Before(cj.Status.LastScheduleTime) {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "LastRunFailed"
		cond.Message = fmt.Sprintf("the last scheduled run at %s failed", cj.Status.LastScheduleTime.Format(time.RFC3339))
		return cond
	}

	// Nothing above matched, so the latest scheduled run succeeded.
	cond.Status = metav1.ConditionTrue
	cond.Reason = "LastRunSucceeded"
	cond.Message = fmt.Sprintf("the last scheduled run at %s succeeded", cj.Status.LastScheduleTime.Format(time.RFC3339))
	return cond
}

// Fixed key names of the install-level credentials Secret (ADR 0014, decision 4). One
// Secret holds either the SMTP pair or the SES pair.
const (
	secretKeySMTPUsername = "username"
	secretKeySMTPPassword = "password"
	secretKeySESAccessKey = "access-key-id"
	secretKeySESSecretKey = "secret-access-key"
)

// credentialsCondition reports whether the configured credentials Secret exists and holds a
// complete key pair (ADR 0014, decision 8). The controller never reads the mail ConfigMap,
// so it does not know whether SMTP or SES is in use and accepts either pair. With no Secret
// configured the condition is True: credentials are optional (SMTP without AUTH, SES with
// ambient credentials such as IRSA).
func (r *CertReportReconciler) credentialsCondition(ctx context.Context) (metav1.Condition, error) {
	cond := metav1.Condition{Type: cyclopsv1alpha1.ConditionCredentialsFound}
	if r.CredentialsSecret == "" {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "NotConfigured"
		cond.Message = "no credentials Secret is configured; delivery uses no static credentials"
		return cond, nil
	}

	var secret corev1.Secret
	err := r.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.CredentialsSecret}, &secret)
	switch {
	case apierrors.IsNotFound(err):
		cond.Status = metav1.ConditionFalse
		cond.Reason = "SecretNotFound"
		cond.Message = fmt.Sprintf("Secret %s/%s does not exist", r.Namespace, r.CredentialsSecret)
	case err != nil:
		return cond, fmt.Errorf("reading credentials Secret %s/%s: %w", r.Namespace, r.CredentialsSecret, err)
	case !hasCredentialPair(&secret):
		cond.Status = metav1.ConditionFalse
		cond.Reason = "SecretKeyMissing"
		cond.Message = fmt.Sprintf("Secret %s/%s needs the keys %q and %q (SMTP) or %q and %q (SES)",
			r.Namespace, r.CredentialsSecret, secretKeySMTPUsername, secretKeySMTPPassword,
			secretKeySESAccessKey, secretKeySESSecretKey)
	default:
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Found"
		cond.Message = fmt.Sprintf("Secret %s/%s has a complete credentials pair", r.Namespace, r.CredentialsSecret)
	}
	return cond, nil
}

// namespacesCondition reports whether every namespace named in spec.namespaces exists (ADR
// 0011). A namespace can be wrong from the start (a typo) or deleted later, in which case the
// report silently covers less, so this runs on every reconcile and on Namespace changes. An
// empty list means "all namespaces" and has nothing to check.
func (r *CertReportReconciler) namespacesCondition(ctx context.Context, cr *cyclopsv1alpha1.CertReport) (metav1.Condition, error) {
	cond := metav1.Condition{Type: cyclopsv1alpha1.ConditionNamespacesFound}
	if cr.Spec.IngressAnnotationMode {
		// Scope comes from annotated Ingresses (ADR 0020); there is no namespace list to check.
		cond.Status = metav1.ConditionTrue
		cond.Reason = "AnnotationMode"
		cond.Message = "not applicable: scope comes from annotated Ingresses"
		return cond, nil
	}
	if len(cr.Spec.Namespaces) == 0 {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "AllNamespaces"
		cond.Message = "spec.namespaces is empty: every namespace is covered"
		return cond, nil
	}

	var missing []string
	for _, name := range cr.Spec.Namespaces {
		var ns corev1.Namespace
		err := r.Get(ctx, client.ObjectKey{Name: name}, &ns)
		switch {
		case apierrors.IsNotFound(err):
			missing = append(missing, name)
		case err != nil:
			// Could not find out: retry rather than record a wrong verdict.
			return cond, fmt.Errorf("reading namespace %q: %w", name, err)
		}
	}

	if len(missing) > 0 {
		// Sorted so the message is stable and does not cause a status write every reconcile.
		slices.Sort(missing)
		cond.Status = metav1.ConditionFalse
		cond.Reason = "NamespaceNotFound"
		cond.Message = "namespaces not found: " + strings.Join(missing, ", ")
		return cond, nil
	}
	cond.Status = metav1.ConditionTrue
	cond.Reason = "Found"
	cond.Message = "all namespaces in spec.namespaces exist"
	return cond, nil
}

// certReportsForNamespace maps a created or deleted Namespace to the CertReports that name it
// in spec.namespaces. A CertReport with an empty list covers everything and has nothing to
// re-check, so it is not enqueued.
func (r *CertReportReconciler) certReportsForNamespace(ctx context.Context, obj client.Object) []reconcile.Request {
	var list cyclopsv1alpha1.CertReportList
	if err := r.List(ctx, &list); err != nil {
		logf.FromContext(ctx).Error(err, "listing CertReports for a Namespace change")
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if slices.Contains(list.Items[i].Spec.Namespaces, obj.GetName()) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}

// hasCredentialPair is true when the Secret holds the SMTP pair or the SES pair, non-empty.
func hasCredentialPair(secret *corev1.Secret) bool {
	has := func(keys ...string) bool {
		for _, k := range keys {
			if len(secret.Data[k]) == 0 {
				return false
			}
		}
		return true
	}
	return has(secretKeySMTPUsername, secretKeySMTPPassword) || has(secretKeySESAccessKey, secretKeySESSecretKey)
}

// warnOnTransition emits a Warning Event when cond turns False or changes its reason or
// message (a second namespace going missing is news). It stays quiet while the condition is
// unchanged, so a broken Secret produces one Event, not one per reconcile. It must run before
// updateStatus, which overwrites the stored condition it compares against. A nil Recorder
// (unit tests) emits nothing.
func (r *CertReportReconciler) warnOnTransition(cr *cyclopsv1alpha1.CertReport, cond metav1.Condition) {
	if r.Recorder == nil || cond.Status != metav1.ConditionFalse {
		return
	}
	if prev := meta.FindStatusCondition(cr.Status.Conditions, cond.Type); prev != nil &&
		prev.Status == metav1.ConditionFalse && prev.Reason == cond.Reason && prev.Message == cond.Message {
		return
	}
	r.Recorder.Eventf(cr, nil, corev1.EventTypeWarning, cond.Reason, "Reconcile", "%s", cond.Message)
}

// updateStatus applies the conditions and writes status once, and only if it changed, so
// a reconcile does not trigger another reconcile. SetStatusCondition keeps
// lastTransitionTime unless the condition's status flips.
//
// Status is written with a merge patch, not an update. An update sends back the
// resourceVersion that was read, and the API server refuses it if the object has moved on.
// cr comes from the manager's cache, which can lag a moment behind the API server, and our own
// CronJob changes trigger a second reconcile within milliseconds, so such refusals happen.
// A patch carries no resourceVersion, so a stale copy cannot make it fail. That is safe here
// because this controller is the only writer of status.
func (r *CertReportReconciler) updateStatus(ctx context.Context, cr *cyclopsv1alpha1.CertReport, conds ...metav1.Condition) error {
	original := cr.DeepCopy()
	for _, c := range conds {
		c.ObservedGeneration = cr.Generation
		meta.SetStatusCondition(&cr.Status.Conditions, c)
	}
	if equality.Semantic.DeepEqual(original.Status, cr.Status) {
		return nil
	}
	return r.Status().Patch(ctx, cr, client.MergeFrom(original))
}

// maxCronJobNameLen is the longest CronJob name Kubernetes accepts: the controller
// appends an 11-character suffix to build Job names, and those must fit in 63.
const maxCronJobNameLen = 52

// cronJobName is the name of the CronJob owned by the CertReport crName. CertReport names
// can be up to 253 characters, so a long one is cut and given a hash to stay unique.
func cronJobName(crName string) string {
	name := "cyclops-" + crName
	if len(name) <= maxCronJobNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(crName))
	suffix := hex.EncodeToString(sum[:])[:8]
	return name[:maxCronJobNameLen-len(suffix)-1] + "-" + suffix
}

// mutateCronJob sets the fields the controller owns on cj and leaves everything else (the
// API server's defaults) alone, so an unchanged CertReport produces no update. It copies the
// schedule and run settings from the spec, and builds the report Pod: the same image as the
// controller, started in report mode, running as the read-only report ServiceAccount, with
// the mail settings injected as environment variables.
func (r *CertReportReconciler) mutateCronJob(cj *batchv1.CronJob, cr *cyclopsv1alpha1.CertReport) error {
	if cj.Labels == nil {
		cj.Labels = map[string]string{}
	}
	cj.Labels["app.kubernetes.io/name"] = "cyclops"
	cj.Labels["app.kubernetes.io/managed-by"] = "cyclops-controller"

	spec := &cj.Spec
	spec.Schedule = cr.Spec.Schedule
	spec.Suspend = cr.Spec.Suspend
	spec.StartingDeadlineSeconds = cr.Spec.StartingDeadlineSeconds
	spec.SuccessfulJobsHistoryLimit = cr.Spec.SuccessfulJobsHistoryLimit
	spec.FailedJobsHistoryLimit = cr.Spec.FailedJobsHistoryLimit
	// Forbid: a slow run must not overlap the next one, or the same email goes out twice.
	spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
	spec.JobTemplate.Spec.BackoffLimit = cr.Spec.BackoffLimit

	pod := &spec.JobTemplate.Spec.Template.Spec
	pod.ServiceAccountName = r.ReportServiceAccount
	pod.RestartPolicy = corev1.RestartPolicyNever
	if len(pod.Containers) != 1 {
		pod.Containers = []corev1.Container{{}}
	}
	c := &pod.Containers[0]
	c.Name = "report"
	c.Image = r.Image
	// The image entrypoint is the manager binary; the first argument selects report mode.
	// --namespace tells report mode where a custom template ConfigMap lives.
	c.Args = []string{"report", "--certreport=" + cr.Name, "--namespace=" + r.Namespace}
	if r.DefaultTemplateConfigMap != "" {
		c.Args = append(c.Args, "--default-template-configmap="+r.DefaultTemplateConfigMap)
	}
	c.EnvFrom = r.mailEnvFrom()
	c.Env = r.mailEnv()

	return controllerutil.SetControllerReference(cr, cj, r.Scheme)
}

// mailEnvFrom wires the install-level mail ConfigMap into the report Pod (ADR 0014).
func (r *CertReportReconciler) mailEnvFrom() []corev1.EnvFromSource {
	if r.MailConfigMap == "" {
		return nil
	}
	return []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: r.MailConfigMap},
	}}}
}

// mailEnv wires the credentials Secret into the report Pod with the fixed key names of
// ADR 0014. Every reference is optional: one Secret holds either the SMTP pair or the SES
// pair, and SES may use no static keys at all (ambient credentials).
func (r *CertReportReconciler) mailEnv() []corev1.EnvVar {
	if r.CredentialsSecret == "" {
		return nil
	}
	fromSecret := func(envName, key string) corev1.EnvVar {
		optional := true
		return corev1.EnvVar{Name: envName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: r.CredentialsSecret},
			Key:                  key,
			Optional:             &optional,
		}}}
	}
	return []corev1.EnvVar{
		fromSecret(email.EnvSMTPUsername, secretKeySMTPUsername),
		fromSecret(email.EnvSMTPPassword, secretKeySMTPPassword),
		fromSecret("AWS_ACCESS_KEY_ID", secretKeySESAccessKey),
		fromSecret("AWS_SECRET_ACCESS_KEY", secretKeySESSecretKey),
	}
}

// certReportsForSecret maps a change of the credentials Secret to every CertReport, since
// they all share it (ADR 0014). Other Secrets are ignored.
func (r *CertReportReconciler) certReportsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.CredentialsSecret == "" || obj.GetNamespace() != r.Namespace || obj.GetName() != r.CredentialsSecret {
		return nil
	}
	var list cyclopsv1alpha1.CertReportList
	if err := r.List(ctx, &list); err != nil {
		logf.FromContext(ctx).Error(err, "listing CertReports for a credentials Secret change")
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}

// SetupWithManager registers the controller with the Manager and chooses what wakes it up:
//   - a CertReport changing (For);
//   - a CronJob it owns changing or being deleted by hand (Owns), so it is put back;
//   - the credentials Secret changing (Watches), so CredentialsFound follows it;
//   - a Namespace being created or deleted (Watches), so NamespacesFound follows it.
func (r *CertReportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO: Owns(&batchv1.Job{}), and a watch on the template ConfigMap mapped back to
	// CertReports.
	return ctrl.NewControllerManagedBy(mgr).
		For(&cyclopsv1alpha1.CertReport{}).
		Owns(&batchv1.CronJob{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.certReportsForSecret)).
		// Only creation and deletion change whether a namespace exists; skip the noisy updates.
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.certReportsForNamespace),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc:  func(event.UpdateEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
			})).
		Named("certreport").
		Complete(r)
}
