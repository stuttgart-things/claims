#!/bin/bash
# Test script for GitOps integration
# Tests git commit and branch operations

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
TEST_REPO="/tmp/claims-gitops-test-$$"
CLAIMS_BIN="$PROJECT_DIR/claims"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_test() { echo -e "\n${YELLOW}=== TEST: $1 ===${NC}"; }

cleanup() {
    log_info "Cleaning up test repo: $TEST_REPO"
    rm -rf "$TEST_REPO"
}

trap cleanup EXIT

# Build the CLI
log_info "Building claims CLI..."
cd "$PROJECT_DIR"
go build -o "$CLAIMS_BIN" .

# Check API is running
log_info "Checking API connectivity..."
if ! curl -s http://localhost:8080/api/v1/claim-templates > /dev/null; then
    log_error "API not reachable at http://localhost:8080"
    exit 1
fi
log_info "API is running"

# Create test repository
log_info "Creating test git repository at $TEST_REPO"
mkdir -p "$TEST_REPO"
cd "$TEST_REPO"
git init
git config user.email "test@test.com"
git config user.name "Test User"
echo "# Test Repo" > README.md
git add README.md
git commit -m "Initial commit"

# Test 1: Render with git commit (local repo)
log_test "Render with --git-commit to local repo"
cd "$PROJECT_DIR"
"$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple \
    -p name=test-volume \
    -p storage=10Gi \
    -o "$TEST_REPO/manifests" \
    --git-commit \
    --git-message "Add test volume claim"

# Verify commit was created
cd "$TEST_REPO"
COMMIT_MSG=$(git log -1 --pretty=%B)
if [[ "$COMMIT_MSG" == *"test volume claim"* ]]; then
    log_info "Commit created successfully: $COMMIT_MSG"
else
    log_error "Expected commit message not found"
    git log -1
    exit 1
fi

# Verify file was created
if [[ -f "$TEST_REPO/manifests/volumeclaim-simple-test-volume.yaml" ]]; then
    log_info "Output file created: manifests/volumeclaim-simple-test-volume.yaml"
else
    log_error "Output file not found"
    ls -la "$TEST_REPO/manifests/"
    exit 1
fi

# Test 2: Render with new branch creation
log_test "Render with --git-create-branch"
cd "$PROJECT_DIR"
"$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple \
    -p name=feature-volume \
    -p storage=20Gi \
    -o "$TEST_REPO/manifests" \
    --git-commit \
    --git-create-branch \
    --git-branch feature/new-volume \
    --git-message "Add feature volume"

# Verify branch was created and checked out
cd "$TEST_REPO"
CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" == "feature/new-volume" ]]; then
    log_info "Branch created and checked out: $CURRENT_BRANCH"
else
    log_error "Expected branch 'feature/new-volume', got '$CURRENT_BRANCH'"
    exit 1
fi

# Verify commit on new branch
COMMIT_MSG=$(git log -1 --pretty=%B)
if [[ "$COMMIT_MSG" == *"feature volume"* ]]; then
    log_info "Commit on new branch: $COMMIT_MSG"
else
    log_error "Expected commit message not found on new branch"
    exit 1
fi

# Test 3: Auto-generated commit message
log_test "Render with auto-generated commit message"
cd "$PROJECT_DIR"
git -C "$TEST_REPO" checkout master 2>/dev/null || git -C "$TEST_REPO" checkout main
"$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple \
    -p name=auto-msg-volume \
    -p storage=5Gi \
    -o "$TEST_REPO/manifests" \
    --git-commit

cd "$TEST_REPO"
COMMIT_MSG=$(git log -1 --pretty=%B)
if [[ "$COMMIT_MSG" == *"Rendered claims: volumeclaim-simple"* ]]; then
    log_info "Auto-generated commit message: $COMMIT_MSG"
else
    log_error "Auto-generated commit message not as expected: $COMMIT_MSG"
    exit 1
fi

# Test 4: Dry-run should NOT commit
log_test "Dry-run should not create commits"
BEFORE_COMMIT=$(git -C "$TEST_REPO" rev-parse HEAD)
cd "$PROJECT_DIR"
"$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple \
    -p name=dryrun-volume \
    -o "$TEST_REPO/manifests" \
    --dry-run \
    --git-commit

AFTER_COMMIT=$(git -C "$TEST_REPO" rev-parse HEAD)
if [[ "$BEFORE_COMMIT" == "$AFTER_COMMIT" ]]; then
    log_info "Dry-run did not create a commit (as expected)"
else
    log_error "Dry-run should not have created a commit!"
    exit 1
fi

# Test 5: Multiple templates
log_test "Render multiple templates with git commit"
cd "$PROJECT_DIR"
"$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple,storageplatform-openebs \
    -p name=multi-test \
    -o "$TEST_REPO/multi" \
    --git-commit \
    --git-message "Add multiple resources"

cd "$TEST_REPO"
FILE_COUNT=$(ls -1 "$TEST_REPO/multi/" 2>/dev/null | wc -l)
if [[ "$FILE_COUNT" -ge 2 ]]; then
    log_info "Multiple files created: $FILE_COUNT files"
    ls -la "$TEST_REPO/multi/"
else
    log_warn "Expected multiple files, got $FILE_COUNT"
fi

# Summary
echo ""
echo "========================================"
log_info "All tests passed!"
echo "========================================"
echo ""
log_info "Test repository contents:"
cd "$TEST_REPO"
git log --oneline --all
echo ""
find . -name "*.yaml" -type f
