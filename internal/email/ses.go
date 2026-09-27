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
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// charsetUTF8 is declared on both subject and body, so non-ASCII text (e.g.
// the template's em dashes) arrives intact.
const charsetUTF8 = "UTF-8"

// SESConfig is the SES half of the install-level delivery settings.
//
// There are no credential fields. The AWS SDK's default chain finds them:
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY when the optional static-key
// Secret is configured, otherwise IRSA, EKS Pod Identity or an instance
// role (ADR 0014).
type SESConfig struct {
	// Region is required: off EC2 there is no reliable ambient region.
	Region string
}

// sesAPI is the part of the SESv2 client Send uses, so tests can fake it.
type sesAPI interface {
	SendEmail(ctx context.Context, in *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// SES sends through Amazon SES (API v2).
type SES struct {
	client sesAPI
}

var _ Sender = (*SES)(nil)

// NewSES checks cfg and builds an SES client. Credentials are resolved on
// the first Send, not here, so a missing role only shows up when mail is
// actually sent.
func NewSES(ctx context.Context, cfg SESConfig) (*SES, error) {
	// TODO(user): require cfg.Region, then
	// config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region)) and
	// return &SES{client: sesv2.NewFromConfig(awsCfg)}.
	return nil, errors.New("ses: NewSES not implemented")
}

// Send delivers msg with one SendEmail call; SES builds the MIME message.
func (s *SES) Send(ctx context.Context, msg Message) error {
	// TODO(user): parse(msg), then s.client.SendEmail with:
	//   FromEmailAddress: from.String(); Destination.ToAddresses: each
	//   to.String(); Content.Simple with Subject and Body.Html, both with
	//   Charset charsetUTF8. Wrap a send error with %w.
	return errors.New("ses: Send not implemented")
}
