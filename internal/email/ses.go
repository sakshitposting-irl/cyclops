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
	"fmt"

	// config loads credentials and region through the SDK's default chain.
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"

	// sesv2 is the AWS SES API v2 client; its SendEmail call builds and sends the MIME message for us.
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
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
	// client is the real sesv2.Client in production and a fake in tests.
	client sesAPI
}

// compile-time check that *SES satisfies Sender, so a signature drift fails the build, not a caller.

// We try to assign a *SES to a Sender variable. If *SES doesn't implement Sender, this assignment fails and the compiler reports an error.
var _ Sender = (*SES)(nil)

// NewSES checks cfg and builds an SES client. Credentials are resolved on
// the first Send, not here, so a missing role only shows up when mail is
// actually sent.
func NewSES(ctx context.Context, cfg SESConfig) (*SES, error) {
	// placeholder until implemented: fail loudly so a half-built sender can't silently drop mail.

	if cfg.Region == "" {
		return nil, errors.New("ses: NewSES requires cfg.Region")
	}

	// LoadDefaultConfig loads credentials and region through the SDK's default chain.

	// config
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("ses: load AWS config: %w", err)
	}

	client := sesv2.NewFromConfig(awsCfg)
	return &SES{client}, nil
}

// Send delivers msg with one SendEmail call; SES builds the MIME message.
func (s *SES) Send(ctx context.Context, msg Message) error {
	pm, err := parse(msg)
	if err != nil {
		return fmt.Errorf("ses: parse message: %w", err)
	}

	// addresses are bare (no display name); parse has already validated them
	to := make([]string, len(pm.to))
	for i, addr := range pm.to {
		to[i] = addr.Address
	}

	_, err = s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(pm.from.Address),
		Destination:      &types.Destination{ToAddresses: to},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: aws.String(pm.Subject), Charset: aws.String(charsetUTF8)},
				Body: &types.Body{
					Html: &types.Content{Data: aws.String(pm.HTMLBody), Charset: aws.String(charsetUTF8)},
				},
			},
		},
	})

	if err != nil {
		return fmt.Errorf("ses: send email: %w", err)
	}
	return nil
}
