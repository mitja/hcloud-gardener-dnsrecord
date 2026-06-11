#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Mitja Martini
#
# SPDX-License-Identifier: Apache-2.0
#
# Generates a Gardener ControllerDeployment + ControllerRegistration for the
# dnsrecord-hcloud extension by gzip+base64-encoding the Helm chart into the
# ControllerDeployment's helm.rawChart field (mirrors gardener's own
# hack/generate-controller-registration.sh approach).
#
# Usage:
#   hack/generate-controller-registration.sh [CHART_DIR] [OUTPUT_FILE] [IMAGE_TAG]
#
# Defaults:
#   CHART_DIR   = charts/gardener-extension-dnsrecord-hcloud
#   OUTPUT_FILE = example/controller-registration.yaml
#   IMAGE_TAG   = v0.1.0

set -o errexit
set -o nounset
set -o pipefail

NAME="dnsrecord-hcloud"
PROVIDER_TYPE="hcloud-dns"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="${1:-${REPO_ROOT}/charts/gardener-extension-dnsrecord-hcloud}"
OUTPUT_FILE="${2:-${REPO_ROOT}/example/controller-registration.yaml}"
IMAGE_TAG="${3:-v0.1.0}"

if [[ ! -d "${CHART_DIR}" ]]; then
  echo "chart directory ${CHART_DIR} does not exist" >&2
  exit 1
fi

# Tar the chart (with the chart directory as the single top-level entry, like
# helm package does), gzip it, and base64-encode it onto a single line.
CHART_PARENT="$(cd "${CHART_DIR}/.." && pwd)"
CHART_BASE="$(basename "${CHART_DIR}")"

# Prefer GNU tar (gtar) for reproducible output; fall back to the system tar
# (e.g. bsdtar on macOS) which lacks the GNU-only determinism flags.
if command -v gtar >/dev/null 2>&1; then
  RAW_CHART="$(
    gtar --sort=name \
         --mtime='2026-01-01 00:00:00 UTC' \
         --owner=0 --group=0 --numeric-owner \
         -C "${CHART_PARENT}" -czf - "${CHART_BASE}" \
      | base64 | tr -d '\n'
  )"
else
  # COPYFILE_DISABLE stops macOS bsdtar from emitting AppleDouble (._*) files.
  RAW_CHART="$(
    COPYFILE_DISABLE=1 tar --exclude '._*' -C "${CHART_PARENT}" -czf - "${CHART_BASE}" \
      | base64 | tr -d '\n'
  )"
fi

mkdir -p "$(dirname "${OUTPUT_FILE}")"

cat > "${OUTPUT_FILE}" <<EOF
---
apiVersion: core.gardener.cloud/v1
kind: ControllerDeployment
metadata:
  name: ${NAME}
helm:
  rawChart: ${RAW_CHART}
  values:
    image:
      tag: ${IMAGE_TAG}
---
apiVersion: core.gardener.cloud/v1beta1
kind: ControllerRegistration
metadata:
  name: ${NAME}
  annotations:
    security.gardener.cloud/pod-security-enforce: baseline
spec:
  deployment:
    deploymentRefs:
    - name: ${NAME}
  resources:
  - kind: DNSRecord
    type: ${PROVIDER_TYPE}
    # internal/default-domain DNSRecords are reconciled by the seed; this
    # extension must therefore be installed on every seed.
    primary: true
EOF

echo "Wrote ${OUTPUT_FILE} (image tag ${IMAGE_TAG})"
