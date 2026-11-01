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

package webhooks

import (
	"strings"
	"testing"
)

func TestSlackEnvName(t *testing.T) {
	for name, want := range map[string]string{
		"oncall":         "CYCLOPS_SLACK_ONCALL",
		"platform-certs": "CYCLOPS_SLACK_PLATFORM_CERTS",
		"team-2":         "CYCLOPS_SLACK_TEAM_2",
	} {
		if got := SlackEnvName(name); got != want {
			t.Errorf("SlackEnvName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSlackFromEnv(t *testing.T) {
	env := map[string]string{"CYCLOPS_SLACK_ONCALL": "https://hooks.example.com/SECRETSECRET\n"}

	s, err := SlackFromEnv("oncall", func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("SlackFromEnv() error = %v", err)
	}
	if s.url != "https://hooks.example.com/SECRETSECRET" {
		t.Errorf("url = %q, want it trimmed", s.url)
	}

	_, err = SlackFromEnv("platform-certs", func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), `"platform-certs"`) || !strings.Contains(err.Error(), "CYCLOPS_SLACK_PLATFORM_CERTS") {
		t.Fatalf("SlackFromEnv() error = %v, want it to name the target and the variable", err)
	}
}
