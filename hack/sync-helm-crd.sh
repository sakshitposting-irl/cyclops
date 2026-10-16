#!/usr/bin/env bash
# Syncs repo sources into the chart. Wraps the generated CRD (config/crd/bases) as a chart template, gated by crd.enabled and
# optionally annotated helm.sh/resource-policy: keep (crd.keep). Run via `make helm-crd`.
set -euo pipefail
src=config/crd/bases/cyclops.sakshitposting-irl.github.io_certreports.yaml
dst=charts/cyclops/templates/crd.yaml
{
  echo '{{- if .Values.crd.enabled }}'
  # drop the leading "---" and add the keep annotation next to controller-gen's
  sed -e '1{/^---$/d}' \
    -e 's|^  annotations:$|  annotations:\n    {{- if .Values.crd.keep }}\n    helm.sh/resource-policy: keep\n    {{- end }}|' \
    "$src"
  echo '{{- end }}'
} > "$dst"

# The built-in email template ships in the chart too, so chart-only users can see and edit it.
cp internal/render/default.html.tmpl charts/cyclops/files/default.html.tmpl
