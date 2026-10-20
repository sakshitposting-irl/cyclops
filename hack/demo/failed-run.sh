#!/usr/bin/env bash
# Runs the "good -> broken -> fixed" sequence for the ReportSucceeding condition against the
# kind-cyclops cluster and prints a transcript to stdout. Needs the kind setup from the README
# (CertReport kind-test, MailHog, fixtures). Takes about 3 minutes: it waits for real
# scheduled runs. Everything it changes is put back on exit.
#
#   hack/demo/failed-run.sh | tee demo.log && python3 hack/demo/render-gif.py demo.log docs/assets/failed-run.gif
set -euo pipefail

# Every kubectl call in this script is pinned to the kind cluster.
kubectl() { command kubectl --context kind-cyclops "$@"; }

CR=kind-test
CM=cyclops-mail-config
GOOD_HOST=mailhog.mailhog.svc.cluster.local
SHOW="get certreport $CR -o yaml | grep -B4 'type: ReportSucceeding'"

orig_schedule=$(kubectl get certreport "$CR" -o jsonpath='{.spec.schedule}')
orig_namespaces=$(kubectl get certreport "$CR" -o jsonpath='{.spec.namespaces}')
restore() {
  kubectl -n cyclops-system patch cm "$CM" --type merge -p "{\"data\":{\"CYCLOPS_SMTP_HOST\":\"$GOOD_HOST\"}}" >/dev/null
  kubectl patch certreport "$CR" --type merge -p "{\"spec\":{\"schedule\":\"$orig_schedule\",\"namespaces\":$orig_namespaces}}" >/dev/null
}
trap restore EXIT

status() { kubectl get certreport "$CR" -o jsonpath='{.status.conditions[?(@.type=="ReportSucceeding")].status}'; }
wait_for() { # wait_for True|False
  local deadline=$((SECONDS + 200))
  until [ "$(status)" = "$1" ]; do
    ((SECONDS < deadline)) || { echo "timed out waiting for $1" >&2; exit 1; }
    sleep 3
  done
}
say() { printf '# %s\n' "$*"; }
run() { printf '$ %s\n' "$*"; eval "$*"; }

# Setup, not part of the recording: run every minute and cover the namespace with the
# never-issued certificate, so every run really sends mail.
kubectl -n cyclops-system patch cm "$CM" --type merge -p "{\"data\":{\"CYCLOPS_SMTP_HOST\":\"$GOOD_HOST\"}}" >/dev/null
kubectl patch certreport "$CR" --type merge \
  -p '{"spec":{"schedule":"* * * * *","namespaces":["cyclops-test-a","cyclops-test-b"]}}' >/dev/null
wait_for True

echo '### START'
say '1. A good run: the report was emailed'
run "kubectl $SHOW"

say '2. Break the mail server address'
run "kubectl -n cyclops-system patch cm $CM --type merge -p '{\"data\":{\"CYCLOPS_SMTP_HOST\":\"no-such-host.invalid\"}}'"
say 'waiting for the next scheduled run...'
wait_for False
run "kubectl $SHOW"
run "kubectl -n default get events --field-selector reason=LastRunFailed | tail -n 1 | cut -c1-140"
failed_job=$(kubectl -n cyclops-system get jobs -o jsonpath='{range .items[?(@.status.failed)]}{.metadata.name}{"\n"}{end}' | tail -n 1)
run "kubectl -n cyclops-system logs job/$failed_job | grep -o '\"error\":\"[^\"]*\"' | cut -c1-120"

say '3. Fix it'
run "kubectl -n cyclops-system patch cm $CM --type merge -p '{\"data\":{\"CYCLOPS_SMTP_HOST\":\"$GOOD_HOST\"}}'"
say 'waiting for the next scheduled run...'
wait_for True
run "kubectl $SHOW"
