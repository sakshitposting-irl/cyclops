#!/usr/bin/env bash
# Syncs generated and shared sources into the Helm chart (run via `make helm-crd`):
#   - the generated CRD, wrapped in a template gated by crd.enabled and optionally annotated
#     helm.sh/resource-policy: keep (crd.keep);
#   - the built-in email templates (report and welcome), so chart-only users can see and edit them.
# Never edit the chart's copies by hand; they are overwritten.
set -euo pipefail

crd_src=config/crd/bases/cyclops.sakshitposting-irl.github.io_certreports.yaml
crd_dst=charts/cyclops/templates/crd.yaml
{
  echo '{{- if .Values.crd.enabled }}'
  # drop a leading "---" and add the keep annotation next to controller-gen's
  sed -e '1{/^---$/d}' \
    -e 's|^  annotations:$|  annotations:\n    {{- if .Values.crd.keep }}\n    helm.sh/resource-policy: keep\n    {{- end }}|' \
    "$crd_src"
  echo '{{- end }}'
} > "$crd_dst"

cp internal/render/default.html.tmpl charts/cyclops/files/default.html.tmpl
cp internal/render/default-welcome.html.tmpl charts/cyclops/files/default-welcome.html.tmpl
