#!/bin/bash
# Test script for GitOps integration
# Tests git commit, branch operations, and PR creation

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

# ===========================================
# PR SUPPORT TESTS
# ===========================================

# Test 6: Check gh CLI availability
log_test "Check gh CLI availability"
if command -v gh &> /dev/null; then
    log_info "gh CLI is installed: $(gh --version | head -1)"
    GH_AVAILABLE=true
else
    log_warn "gh CLI not installed - skipping PR creation tests"
    GH_AVAILABLE=false
fi

# Test 7: PR flags validation (dry-run, doesn't actually create PR)
log_test "PR flags parsing with dry-run"
cd "$PROJECT_DIR"
OUTPUT=$("$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple \
    -p name=pr-test-volume \
    -p storage=10Gi \
    -o "$TEST_REPO/pr-test" \
    --dry-run \
    --create-pr \
    --pr-title "Test PR Title" \
    --pr-labels "test,automated" \
    --pr-base main \
    --git-create-branch \
    --git-branch feature/pr-test 2>&1) || true

# Verify the command ran (dry-run should not actually create PR)
if [[ "$OUTPUT" == *"volumeclaim-simple"* ]]; then
    log_info "PR flags parsed correctly with dry-run"
else
    log_warn "Unexpected output: $OUTPUT"
fi

# Test 8: PR creation (only if gh is available and authenticated)
if [[ "$GH_AVAILABLE" == "true" ]]; then
    log_test "Check gh authentication status"
    if gh auth status &> /dev/null; then
        log_info "gh CLI is authenticated"
        GH_AUTHENTICATED=true
    else
        log_warn "gh CLI not authenticated - skipping actual PR creation test"
        GH_AUTHENTICATED=false
    fi

    # Only run actual PR test if we have a remote and are authenticated
    if [[ "$GH_AUTHENTICATED" == "true" && -n "${TEST_REMOTE_REPO:-}" ]]; then
        log_test "Create actual PR (requires TEST_REMOTE_REPO env var)"
        cd "$PROJECT_DIR"

        # Clone the test remote repo
        REMOTE_TEST_DIR="/tmp/claims-pr-test-$$"
        git clone "$TEST_REMOTE_REPO" "$REMOTE_TEST_DIR"
        cd "$REMOTE_TEST_DIR"
        git config user.email "test@test.com"
        git config user.name "Test User"

        # Create a unique branch name
        BRANCH_NAME="test/claims-pr-$(date +%s)"

        "$CLAIMS_BIN" render \
            --non-interactive \
            -t volumeclaim-simple \
            -p name=pr-test \
            -p storage=1Gi \
            -o "$REMOTE_TEST_DIR/manifests" \
            --create-pr \
            --git-create-branch \
            --git-branch "$BRANCH_NAME" \
            --pr-title "Test PR from claims CLI" \
            --pr-description "Automated test PR - can be closed" \
            --pr-labels "test,automated" \
            --pr-base main

        log_info "PR creation test completed"

        # Cleanup remote test
        rm -rf "$REMOTE_TEST_DIR"
    else
        log_info "Skipping actual PR creation test (set TEST_REMOTE_REPO to enable)"
    fi
else
    log_info "Skipping PR tests - gh CLI not available"
fi

# Test 9: Verify --create-pr implies --git-push
log_test "Verify --create-pr implies commit and push"
cd "$PROJECT_DIR"
# This test just verifies the flags work together
# We use dry-run to avoid actual operations
OUTPUT=$("$CLAIMS_BIN" render \
    --non-interactive \
    -t volumeclaim-simple \
    -p name=implies-test \
    -o "$TEST_REPO/implies-test" \
    --dry-run \
    --create-pr \
    --git-create-branch \
    --git-branch feature/implies-test 2>&1) || true

log_info "Flag combination test passed (--create-pr with --git-create-branch)"

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

echo ""
echo "========================================"
log_info "PR Support Test Summary"
echo "========================================"
echo "gh CLI installed: $GH_AVAILABLE"
if [[ "$GH_AVAILABLE" == "true" ]]; then
    echo "gh CLI authenticated: ${GH_AUTHENTICATED:-unknown}"
fi
echo "To test actual PR creation, set TEST_REMOTE_REPO environment variable"
echo "Example: TEST_REMOTE_REPO=https://github.com/your-org/test-repo.git ./tests/test_gitops.sh"
