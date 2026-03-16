#!/bin/bash
# +-------------------------------------------------------------------+
# | Copyright IBM Corp. 2025 All Rights Reserved                      |
# | PID 5698-SPR                                                      |
# +-------------------------------------------------------------------+

set -eu -o pipefail
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
readonly REPO_ROOT_DIR=${SCRIPT_DIR%/*}
readonly GIT=$(which git)
readonly YQ=${REPO_ROOT_DIR}/bin/yq

BRANCH_TYPE=""
CURRENT_VERSION=""
declare -i RC_NUMBER=0

function usage() {
	echo "Usage: ${0} flags"
	echo "Flags:"
	echo "  -t, --type version-upgrade|minor-release|major-release|patch-release|rc <old release candidate number>  creates a release branch"
	echo "  -d, --dry-run run the script, do not push the branch"
	echo "  -h, --help prints this message"
	exit 2
}

function validate_environment() {

	if [ "x" == "x${GIT}" ]; then
		echo "Error: GIT must have a value, git needs to be available in your path"
		exit 1
	fi

	#remove all tools or files, including local.mk
	make -f ${REPO_ROOT_DIR}/Makefile clean

	if [ ! -f ${YQ} ]; then
		make -f ${REPO_ROOT_DIR}/Makefile yq
	fi

}

function get_current_version() {
	cat ${REPO_ROOT_DIR}/VERSION
}

function is_git_tree_clean() {
	if [[ "xTRUE" == "x${DRY_RUN:-}" ]]; then
		return
	fi
	local output=$(${GIT} status --porcelain)
	if [ ! -z "${output}" ]; then
		echo "The git working tree has uncommitted files."
		echo "${output}"
		exit 1
	fi
}

function is_current_branch_main() {
	if [[ "xTRUE" == "x${DRY_RUN:-}" ]]; then
		return
	fi
	local branch_name=${GIT_BRANCH_NAME:-}
	if [[ -z ${branch_name} ]]; then
		branch_name=$(git branch --show-current)
	fi
	if [[ -z ${branch_name} ]]; then
		branch_name=$(git rev-parse --abbrev-ref HEAD)
	fi

	if [[ ${branch_name} != "main" ]]; then
		echo "Must be on main branch to execute this script"
		exit 1
	fi
}

function is_current_branch_release() {
	if [[ "xTRUE" == "x${DRY_RUN:-}" ]]; then
		return
	fi
	local branch_name=${GIT_BRANCH_NAME:-}
	if [[ -z ${branch_name} ]]; then
		branch_name=$(git branch --show-current)
	fi
	if [[ -z ${branch_name} ]]; then
		branch_name=$(git rev-parse --abbrev-ref HEAD)
	fi
	if [[ ! ${branch_name} =~ ^release_v[0-9]+(\.[0-9]+)+$ ]]; then
		echo "Must be on a release branch to execute this script"
		exit 1
	fi
}

function make_branch() {
	local branch_name=${1}
	local release_type=${2}

	echo "Making branch '${branch_name}' for version: '${CURRENT_VERSION}'"
	${GIT} fetch --quiet origin

	if ${GIT} show-ref --quiet --verify refs/heads/${branch_name}; then
		echo "A local branch named '${branch_name}' already exists"
		exit 1
	fi

	if ${GIT} show-ref --quiet --verify refs/remotes/origin/${branch_name}; then
		echo "A remote branch named '${branch_name}' already exists"
		exit 1
	fi
	${GIT} checkout -b ${branch_name}

	echo ${branch_name} >${REPO_ROOT_DIR}/.e2e-test-branch
	if [[ "minor-release" != ${release_type} ]] && [[ "major-release" != ${release_type} ]]; then
		${GIT} add ${REPO_ROOT_DIR}/VERSION
	fi

	${GIT} add ${REPO_ROOT_DIR}/.e2e-test-branch
	${GIT} commit -m "feat: creates release branch ${branch_name}" -m "Creates branch for release v${CURRENT_VERSION}" --no-verify

	is_git_tree_clean # This ensures that we added all items to the commit.

	if [[ "xTRUE" == "x${DRY_RUN:-}" ]]; then
		return
	fi
	${GIT} push --set-upstream origin ${branch_name}
}

if [[ ${#} == 0 ]]; then
	usage
fi

declare -a POSITIONAL_ARGS=()

while [[ $# -gt 0 ]]; do
	case ${1} in
	-t | --type)
		RELEASE_TYPE="${2}"
		shift # past argument
		shift # past value
		if [[ ${RELEASE_TYPE} == "rc" ]]; then
			RC_NUMBER=$1
			shift
		fi
		;;
	-d | --dry-run)
		DRY_RUN="TRUE"
		shift # past argument
		;;
	-h | --help)
		usage
		;;
	-* | --*)
		echo "Unknown option $1"
		exit 1
		;;
	*)
		POSITIONAL_ARGS+=("$1") # save positional arg
		shift                   # past argument
		;;
	esac
done

if [[ ${#POSITIONAL_ARGS[*]} -gt 0 ]]; then
	echo "Unexpected number of arguments passed"
	exit 1
fi

validate_environment
is_git_tree_clean

case ${RELEASE_TYPE} in
minor-release)
	is_current_branch_main
	CURRENT_VERSION=$(get_current_version)
	BRANCH_NAME="release_v${CURRENT_VERSION}"
	make_branch ${BRANCH_NAME} ${RELEASE_TYPE}
	;;
major-release)
	is_current_branch_main
	CURRENT_VERSION=$(get_current_version)
	BRANCH_NAME="release_v${CURRENT_VERSION}"
	make_branch ${BRANCH_NAME} ${RELEASE_TYPE}
	;;
patch-release)
	is_current_branch_release
	${SCRIPT_DIR}/increment-version.bash --patch
	CURRENT_VERSION=$(get_current_version)
	BRANCH_NAME="patch_to_v${CURRENT_VERSION}"
	make_branch ${BRANCH_NAME} ${RELEASE_TYPE}
	;;
rc)
	is_current_branch_main
	${SCRIPT_DIR}/increment-version.bash --rc ${RC_NUMBER}
	CURRENT_VERSION=$(get_current_version)
	BRANCH_NAME="v${CURRENT_VERSION}"
	make_branch ${BRANCH_NAME} ${RELEASE_TYPE}
	;;
version-upgrade)
	is_current_branch_main
	CURRENT_VERSION=$(get_current_version)
	BRANCH_NAME="update_to_v${CURRENT_VERSION}"
	make_branch ${BRANCH_NAME} ${RELEASE_TYPE}
	;;
*)
	echo "Unsupported branch type:'${RELEASE_TYPE}'"
	usage
	;;
esac
