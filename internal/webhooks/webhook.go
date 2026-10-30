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

// Package webhooks delivers the report to a webhook receiver (ADR 0007, ADR 0017).
// Slack is the first implementation.
package webhooks

import (
	"context"

	"github.com/sakshitposting-irl/cyclops/internal/render"
)

// Sender delivers one report to one receiver. An implementation is built for a single
// target and holds everything it needs to reach it (the URL today, and whatever
// authentication ADR 0017 Q3 settles on), so Send takes only the report.
//
// data is the same value the email templates are executed with, so Welcome is set for the
// one-time welcome message (ADR 0015) and Findings may be empty. Each implementation builds
// the payload its receiver expects from data.
//
// The target's URL may itself be a credential (a Slack incoming webhook URL is). An
// implementation must not put it in a returned error or a log line, including the errors
// of its own constructor.
//
// Send retries transient failures itself and returns one error, once it gives up or the
// failure is not transient. It returns early with ctx's error if ctx is done.
//
// Open under ADR 0017: how the receiver authenticates cyclops (Q3), which failures are
// transient and how often to retry (Q4).
type Sender interface {
	Send(ctx context.Context, data render.TemplateData) error
}
