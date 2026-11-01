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

// env.go names the report Pod's environment variable for each Slack target, and builds the
// target's Sender from it (ADR 0017).

package webhooks

import (
	"fmt"
	"strings"
)

// envSlackPrefix starts the name of every Slack target's URL variable.
const envSlackPrefix = "CYCLOPS_SLACK_"

// SlackEnvName is the environment variable that holds the URL of the Slack target called name:
// "oncall" is CYCLOPS_SLACK_ONCALL, "platform-certs" is CYCLOPS_SLACK_PLATFORM_CERTS. The
// controller calls it when it wires the Pod and report mode when it reads, so the two can't
// disagree. The CRD allows only lowercase letters, digits and "-" in a name, so two names never
// map to the same variable.
func SlackEnvName(name string) string {
	return envSlackPrefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// SlackFromEnv builds the Sender for the Slack target called name from its environment variable,
// read through getenv (os.Getenv in the report binary). A missing variable names the target, so
// a Secret without a url key is easy to find.
func SlackFromEnv(name string, getenv func(string) string) (*Slack, error) {
	env := SlackEnvName(name)
	url := getenv(env)
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("slack target %q: %s is empty: does its urlSecretRef Secret exist with the key %q?",
			name, env, SlackSecretKey)
	}
	return NewSlack(name, url)
}

// SlackSecretKey is the key of a target's Secret that holds the webhook URL (ADR 0017).
const SlackSecretKey = "url"
