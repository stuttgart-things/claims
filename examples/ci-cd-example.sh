#!/bin/bash
# CI/CD Pipeline Example
#
# This script demonstrates how to use claims in a CI/CD pipeline
# (e.g., GitHub Actions, GitLab CI, Jenkins)

set -e

# Configuration (typically set as environment variables in CI)
export CLAIM_API_URL="${CLAIM_API_URL:-http://claim-machinery:8080}"
export GIT_USER="${GIT_USER:-ci-bot}"
# GIT_TOKEN or GITHUB_TOKEN should be set as a secret

# Parameters
PARAMS_FILE="${1:-examples/multi-template.yaml}"
OUTPUT_DIR="${2:-./manifests}"
BRANCH_NAME="infra/update-$(date +%Y%m%d-%H%M%S)"

echo "=== Claims CI/CD Pipeline ==="
echo "API URL: $CLAIM_API_URL"
echo "Params file: $PARAMS_FILE"
echo "Output directory: $OUTPUT_DIR"
echo "Branch: $BRANCH_NAME"
echo ""

# Step 1: Validate params file exists
if [ ! -f "$PARAMS_FILE" ]; then
    echo "ERROR: Params file not found: $PARAMS_FILE"
    exit 1
fi

# Step 2: Dry run to verify templates render correctly
echo "=== Dry Run ==="
claims render --non-interactive \
    -f "$PARAMS_FILE" \
    -o "$OUTPUT_DIR" \
    --dry-run

echo ""
echo "=== Rendering and Creating PR ==="

# Step 3: Render, commit, push, and create PR
claims render --non-interactive \
    -f "$PARAMS_FILE" \
    -o "$OUTPUT_DIR" \
    --git-create-branch \
    --git-branch "$BRANCH_NAME" \
    --create-pr \
    --pr-title "Infrastructure Update - $(date +%Y-%m-%d)" \
    --pr-description "Automated infrastructure update from CI/CD pipeline.

## Changes
- Rendered templates from: $PARAMS_FILE
- Output directory: $OUTPUT_DIR

## Rendered by
- Pipeline: $CI_PIPELINE_ID
- Commit: $CI_COMMIT_SHA" \
    --pr-labels "automated,infrastructure"

echo ""
echo "=== Done ==="
echo "PR created successfully!"
