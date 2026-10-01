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
	"strconv"
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
	switch Provider(getenv(EnvProvider)) {
	case ProviderSMTP:
		cfg := SMTPConfig{
			Host:     getenv(EnvSMTPHost),
			TLS:      TLSMode(getenv(EnvSMTPTLS)),
			Username: getenv(EnvSMTPUsername),
			Password: getenv(EnvSMTPPassword),
		}
		// the port is optional: empty keeps 0 so NewSMTP applies its default
		if rawPort := getenv(EnvSMTPPort); rawPort != "" {
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid port %q: %w", EnvSMTPPort, rawPort, err)
			}
			cfg.Port = port
		}
		return NewSMTP(cfg)
	case ProviderSES:
		return NewSES(ctx, SESConfig{Region: getenv(EnvSESRegion)})
	default:
		return nil, fmt.Errorf("%s: must be %s or %s, got %q", EnvProvider, ProviderSES, ProviderSMTP, getenv(EnvProvider))
	}
}
