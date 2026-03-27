#!/bin/bash
# +-------------------------------------------------------------------+
# | Copyright IBM Corp. 2025 All Rights Reserved                      |
# | PID 5698-SPR                                                      |
# +-------------------------------------------------------------------+
#
# Unit test for hack/create-branch.bash
#
# Copies the files the script modifies into a temp directory and
# runs the script in dry-run mode (-d) with fake git, make, and yq
# binaries so that no real repo mutations occur.
# The tests assert the VERSION file and for patch-release .e2e-test-branch
# modifications for each supported branch type.
#
set -eu -o pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR%/*}"
readonly YQ="${REPO_ROOT}/bin/yq"

# ── helpers ──────────────────────────────────────────────────────────────────

PASS=0
FAIL=0

pass() {
	echo "  PASS: $*"
	PASS=$((PASS + 1))
}
fail() {
	echo "  FAIL: $*"
	FAIL=$((FAIL + 1))
}

assert_eq() {
	local description="$1"
	local expected="$2"
	local actual="$3"
	if [ "${expected}" = "${actual}" ]; then
		pass "${description}"
	else
		fail "${description} — expected '${expected}', got '${actual}'"
	fi
}

assert_file_exists() {
	local description="$1"
	local file_path="$2"
	if [ -f "${file_path}" ]; then
		pass "${description}"
	else
		fail "${description} — file '${file_path}' does not exist"
	fi
}

# ── setup ────────────────────────────────────────────────────────────────────

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "Test temp directory: ${TMPDIR}"

# Mirror the directory structure expected by create-branch.bash.
# The script lives at hack/create-branch.bash and derives
# REPO_ROOT_DIR via ${SCRIPT_DIR%/*} (one level up).
mkdir -p \
	"${TMPDIR}/hack" \
	"${TMPDIR}/bin"

# Symlink the scripts under test and their dependencies
ln -s "${REPO_ROOT}/hack/create-branch.bash" "${TMPDIR}/hack/create-branch.bash"
ln -s "${REPO_ROOT}/hack/get-version.bash" "${TMPDIR}/hack/get-version.bash"
ln -s "${REPO_ROOT}/hack/increment-version.bash" "${TMPDIR}/hack/increment-version.bash"
ln -s "${REPO_ROOT}/bin/yq" "${TMPDIR}/bin/yq"

# Fixed git overrides used across all tests
export GIT_SHORT_HASH="abc1234"

# Minimal Makefile — only needs clean and yq targets
cat >"${TMPDIR}/Makefile" <<'EOF'
.PHONY: clean yq print-%
clean:
	@echo "Cleaning..."
yq:
	@echo "YQ already exists"
print-%:
	@echo "$* = $($*)"
EOF

# Fake git binary
# - show-ref returns 1 so the script believes the branch does not yet exist
# - status --porcelain returns empty (clean working tree)
# - all mutating commands (fetch, checkout, add, commit, push) are no-ops
# - branch / rev-parse honour GIT_BRANCH_NAME for version resolution
cat >"${TMPDIR}/bin/git" <<'EOF'
#!/bin/bash
case "$1" in
fetch)    exit 0 ;;
show-ref) exit 1 ;;   # branch does not exist — allows make_branch to proceed
checkout) exit 0 ;;
add)      exit 0 ;;
commit)   exit 0 ;;
push)     exit 0 ;;
status)   echo ""; exit 0 ;;   # clean working tree
branch)   echo "${GIT_BRANCH_NAME:-main}"; exit 0 ;;
rev-parse)
	if [[ "$2" == "--abbrev-ref" ]]; then
		echo "${GIT_BRANCH_NAME:-main}"
	elif [[ "$2" == "--short=7" ]]; then
		echo "${GIT_SHORT_HASH:-abc1234}"
	fi
	exit 0 ;;
esac
exit 0
EOF
chmod +x "${TMPDIR}/bin/git"

# Fake make — no-op for most targets
cat >"${TMPDIR}/bin/make" <<'EOF'
#!/bin/bash
exit 0
EOF
chmod +x "${TMPDIR}/bin/make"

# Prepend our fake bin/ so all fakes shadow the real binaries
export PATH="${TMPDIR}/bin:${PATH}"

# Preserve originals for reset between test runs
readonly ORIG_VERSION="${TMPDIR}/VERSION.orig"

# ── helpers ───────────────────────────────────────────────────────────────────

reset_configs() {
	if [ -f "${ORIG_VERSION}" ]; then
		cp "${ORIG_VERSION}" "${TMPDIR}/VERSION"
	fi
	rm -f "${TMPDIR}/.e2e-test-branch"
}

# Run the script from TMPDIR in dry-run mode so git push is skipped.
# Caller sets VERSION file and GIT_BRANCH_NAME before calling.
#
# Exit-code note: the last statement in make_branch is:
#   [[ "xTRUE" == "x${DRY_RUN}" ]] && git push ...
# When DRY_RUN=TRUE the condition is false, so the script exits 1.
# Exit codes 0 and 1 are therefore both acceptable; anything higher
# indicates a genuine error.
run_script() {
	local rc=0
	(cd "${TMPDIR}" && "${TMPDIR}/hack/create-branch.bash" -d "$@") || rc=$?
	if [[ ${rc} -gt 1 ]]; then
		echo "  ERROR: create-branch.bash exited with unexpected code ${rc}" >&2
		exit "${rc}"
	fi
}

# ── Branch-type tests ─────────────────────────────────────────────────────────
#
# create-branch.bash behaviour per branch type:
#
#   minor-release  → branch: release_v<version>
#                    VERSION file unchanged
#
#   major-release  → same as minor-release
#
#   patch-release  → branch: patch_to_v<version>
#                    VERSION file incremented (patch)
#                    .e2e-test-branch created with branch name
#
#   rc             → branch: v<version>-rc.<n>
#                    VERSION file incremented (rc)
#
#   version-upgrade → branch: update_to_v<version>
#                     VERSION file unchanged
#

echo ""
echo "════════════════════════════════════════════════════════════════════════"
echo "Branch-type tests"
echo "════════════════════════════════════════════════════════════════════════"

# ── Scenario 1: minor-release ─────────────────────────────────────────────────
# VERSION=1.2.3 → branch release_v1.2.3
# VERSION file should remain 1.2.3

echo ""
echo "--- Scenario: minor-release (VERSION=1.2.3) ---"
reset_configs
echo "1.2.3" >"${TMPDIR}/VERSION"
cp "${TMPDIR}/VERSION" "${ORIG_VERSION}"
export GIT_BRANCH_NAME="release_v1.2.3"
run_script -t minor-release
unset GIT_BRANCH_NAME

assert_eq "minor-release: VERSION unchanged" \
	"1.2.3" \
	"$(cat "${TMPDIR}/VERSION")"

# ── Scenario 2: major-release ─────────────────────────────────────────────────
# Same behaviour as minor-release

echo ""
echo "--- Scenario: major-release (VERSION=2.0.0) ---"
reset_configs
echo "2.0.0" >"${TMPDIR}/VERSION"
cp "${TMPDIR}/VERSION" "${ORIG_VERSION}"
export GIT_BRANCH_NAME="release_v2.0.0"
run_script -t major-release
unset GIT_BRANCH_NAME

assert_eq "major-release: VERSION unchanged" \
	"2.0.0" \
	"$(cat "${TMPDIR}/VERSION")"

# ── Scenario 3: patch-release ─────────────────────────────────────────────────
# increment-version.bash --patch on 1.2.3 → 1.2.4
# branch = patch_to_v1.2.4

echo ""
echo "--- Scenario: patch-release (VERSION=1.2.3 → 1.2.4 after increment) ---"
reset_configs
echo "1.2.3" >"${TMPDIR}/VERSION"
cp "${TMPDIR}/VERSION" "${ORIG_VERSION}"
export GIT_BRANCH_NAME="patch_to_v1.2.4"
run_script -t patch-release
unset GIT_BRANCH_NAME

assert_eq "patch-release: VERSION incremented" \
	"1.2.4" \
	"$(cat "${TMPDIR}/VERSION")"
assert_file_exists "patch-release: .e2e-test-branch created" \
	"${TMPDIR}/.e2e-test-branch"
assert_eq "patch-release: .e2e-test-branch content" \
	"patch_to_v1.2.4" \
	"$(cat "${TMPDIR}/.e2e-test-branch")"

# ── Scenario 4: rc ────────────────────────────────────────────────────────────
# increment-version.bash --rc 1 on 1.2.3-rc.1 (RC_NUMBER=1) → ((++RC_NUMBER))=2 → 1.2.3-rc.2
# branch = v1.2.3-rc.2

echo ""
echo "--- Scenario: rc (VERSION=1.2.3, RC_NUMBER=1 → 1.2.3-rc.2 after increment) ---"
reset_configs
echo "1.2.3-rc.1" >"${TMPDIR}/VERSION"
cp "${TMPDIR}/VERSION" "${ORIG_VERSION}"
export GIT_BRANCH_NAME="v1.2.3-rc.2"
run_script -t rc 1
unset GIT_BRANCH_NAME

assert_eq "rc: VERSION incremented" \
	"1.2.3-rc.2" \
	"$(cat "${TMPDIR}/VERSION")"

# ── Scenario 5: version-upgrade ───────────────────────────────────────────────
# VERSION=1.2.4 → branch update_to_v1.2.4
# VERSION file should remain 1.2.4

echo ""
echo "--- Scenario: version-upgrade (VERSION=1.2.4) ---"
reset_configs
echo "1.2.4" >"${TMPDIR}/VERSION"
cp "${TMPDIR}/VERSION" "${ORIG_VERSION}"
export GIT_BRANCH_NAME="update_to_v1.2.4"
run_script -t version-upgrade
unset GIT_BRANCH_NAME

assert_eq "version-upgrade: VERSION unchanged" \
	"1.2.4" \
	"$(cat "${TMPDIR}/VERSION")"

# ── summary ───────────────────────────────────────────────────────────────────

echo ""
echo "────────────────────────────────────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"
echo "────────────────────────────────────────────────────────────────────────"

if [ "${FAIL}" -gt 0 ]; then
	exit 1
fi
