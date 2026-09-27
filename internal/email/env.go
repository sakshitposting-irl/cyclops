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

package email

import (
	"context"
	"fmt"
)

// Environment variables the report Pod is configured with (ADR 0014). The
// non-secret ones come from the install-level ConfigMap via envFrom; the
// SMTP username and password come from the credentials Secret via
// secretKeyRef. SES static keys, if any, use the AWS SDK's own
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, which the SDK reads itself.
const (
	EnvProvider     = "CYCLOPS_EMAIL_PROVIDER"
	EnvSMTPHost     = "CYCLOPS_SMTP_HOST"
	EnvSMTPPort     = "CYCLOPS_SMTP_PORT"
	EnvSMTPTLS      = "CYCLOPS_SMTP_TLS"
	EnvSMTPUsername = "CYCLOPS_SMTP_USERNAME"
	EnvSMTPPassword = "CYCLOPS_SMTP_PASSWORD"
	EnvSESRegion    = "CYCLOPS_SES_REGION"
)

// Provider is the install-level choice of transport (ADR 0014).
type Provider string

const (
	ProviderSES  Provider = "SES"
	ProviderSMTP Provider = "SMTP"
)

// FromEnv builds the configured Sender from environment variables, read
// through getenv (os.Getenv in the report binary).
func FromEnv(ctx context.Context, getenv func(string) string) (Sender, error) {
	// TODO(user): switch on Provider(getenv(EnvProvider)):
	//   - SMTP: build SMTPConfig from the CYCLOPS_SMTP_* variables (the port
	//     is optional; a non-number is an error) and return NewSMTP(cfg).
	//   - SES: return NewSES(ctx, SESConfig{Region: getenv(EnvSESRegion)}).
	//   - unset or anything else: an error naming EnvProvider.
	return nil, fmt.Errorf("%s: FromEnv not implemented", EnvProvider)
}
