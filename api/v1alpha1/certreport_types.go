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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Condition types set by the controller in status.conditions (ADR 0018).
const (
	// ConditionScheduleValid is False when the API server rejects the CronJob
	// create/update for spec.schedule; the message carries its error.
	ConditionScheduleValid = "ScheduleValid"
	// ConditionNamespacesFound is False when a name in spec.namespaces does not
	// exist (ADR 0011).
	ConditionNamespacesFound = "NamespacesFound"
	// ConditionCredentialsFound is False when the mail credentials Secret is
	// missing (ADR 0014).
	ConditionCredentialsFound = "CredentialsFound"
	// ConditionTemplateValid is False when the referenced template ConfigMap or
	// key is missing, or the template fails to parse (ADR 0005).
	ConditionTemplateValid = "TemplateValid"
	// ConditionReportSucceeding is False when the last scheduled report run failed
	// (ADR 0019). Driven by the owned CronJob's status, not by watching Jobs.
	ConditionReportSucceeding = "ReportSucceeding"
	// ConditionWelcomeSent is True once the welcome email Job has succeeded
	// (ADR 0015).
	ConditionWelcomeSent = "WelcomeSent"
)

// NotifierType selects which delivery channel a notifier entry uses (ADR 0007).
// +kubebuilder:validation:Enum=Email
type NotifierType string

const (
	// NotifierTypeEmail sends the report by email (SES or SMTP, ADR 0008).
	NotifierTypeEmail NotifierType = "Email"
)

// CertReportSpec defines the desired state of CertReport (ADR 0018).
type CertReportSpec struct {
	// schedule is a cron expression (UTC) for when the report runs. It is copied
	// to the owned CronJob. The CRD only does a loose shape check; the API server
	// has the final word, and a rejected schedule is reported through the
	// ScheduleValid condition.
	// +kubebuilder:default="0 8 * * *"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^(@(yearly|annually|monthly|weekly|daily|midnight|hourly)|\S+(\s+\S+){4})$`
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// suspend pauses the report: the owned CronJob is created with suspend set
	// and no further runs fire until it is cleared.
	// +kubebuilder:default=false
	// +optional
	Suspend *bool `json:"suspend,omitempty"`

	// startingDeadlineSeconds is how late (in seconds) a run may start after its
	// scheduled time before it is skipped.
	// +kubebuilder:default=3600
	// +kubebuilder:validation:Minimum=1
	// +optional
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`

	// successfulJobsHistoryLimit is how many finished successful Jobs to keep.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=0
	// +optional
	SuccessfulJobsHistoryLimit *int32 `json:"successfulJobsHistoryLimit,omitempty"`

	// failedJobsHistoryLimit is how many finished failed Jobs to keep.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	FailedJobsHistoryLimit *int32 `json:"failedJobsHistoryLimit,omitempty"`

	// backoffLimit is how many times a failed report Job is retried. Keep it low:
	// a retried email Job can send the same email twice (ADR 0015).
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// namespaces limits the report to Certificates in these namespaces. Omitted
	// or empty means all namespaces.
	// +listType=set
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// emailTemplateConfigMapRef points at a ConfigMap holding a custom email
	// template. Omitted means the built-in template.
	// +optional
	EmailTemplateConfigMapRef *TemplateConfigMapRef `json:"emailTemplateConfigMapRef,omitempty"`

	// notifiers lists where the report is delivered.
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +required
	Notifiers []Notifier `json:"notifiers"`
}

// TemplateConfigMapRef names a key in a ConfigMap in the cyclops namespace.
type TemplateConfigMapRef struct {
	// name of the ConfigMap.
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`

	// key within the ConfigMap that holds the template.
	// +kubebuilder:validation:MinLength=1
	// +required
	Key string `json:"key"`
}

// Notifier is one delivery channel. Exactly the block named by type is set.
// +kubebuilder:validation:XValidation:rule="self.type == 'Email' ? has(self.email) : !has(self.email)",message="the block named by type must be set, and no other"
type Notifier struct {
	// type selects the channel.
	// +required
	Type NotifierType `json:"type"`

	// email configures an Email notifier.
	// +optional
	Email *EmailNotifier `json:"email,omitempty"`
}

// EmailNotifier holds the per-report email settings. Provider and credentials
// are install-level (ADR 0014). Addresses are bare, validated by parse() in
// internal/email, not by the CRD.
type EmailNotifier struct {
	// from is the sender address.
	// +kubebuilder:validation:MinLength=1
	// +required
	From string `json:"from"`

	// to lists the recipient addresses.
	// +required
	To []string `json:"to"`
}

// CertReportStatus defines the observed state of CertReport.
type CertReportStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the CertReport resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspend`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CertReport is the Schema for the certreports API
type CertReport struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of CertReport
	// +required
	Spec CertReportSpec `json:"spec"`

	// status defines the observed state of CertReport
	// +optional
	Status CertReportStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CertReportList contains a list of CertReport
type CertReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CertReport `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &CertReport{}, &CertReportList{})
		return nil
	})
}
