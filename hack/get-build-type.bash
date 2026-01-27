#!/bin/bash
# +-------------------------------------------------------------------+
# | Copyright IBM Corp. 2025 All Rights Reserved                      |
# | PID 5698-SPR                                                      |
# +-------------------------------------------------------------------+
set -e
function branch_name_check() {
	local branch_name=${1}
	if [[ ${branch_name} =~ ^release_[0-9]+(\_[0-9]+)+$ ||
		${branch_name} =~ ^release_v[0-9]+(\.[0-9]+)+$ ||
		${branch_name} =~ ^v[0-9](\.[0-9]+)+-rc\.[0-9]+$ ]]; then
		echo "release"
	elif [[ ${branch_name} =~ ^v[0-9]+\.[0-9]+$ ||
		${branch_name} == "main" ]]; then
		echo "development"
	else
		echo "pr"
	fi
}
function use_git() {
	local short_hash=$(git rev-parse --short=7 HEAD)
	local branch_name=$(git branch --show-current)
	if [[ -z ${branch_name} ]]; then
		branch_name=$(git rev-parse --abbrev-ref HEAD)
	fi
	branch_name_check ${branch_name}
}
function use_travis() {
	if [[ ${TRAVIS_PULL_REQUEST} == "false" ]]; then
		branch_name_check ${TRAVIS_BRANCH}
		return
	fi
	if [[ ${TRAVIS_PULL_REQUEST} != "false" ]]; then
		# Use the PR branch name, as the PR is against the main
		branch_name_check ${TRAVIS_PULL_REQUEST_BRANCH}
	fi
}
# if TRAVIS_PULL_REQUEST environment variable is not present
if [[ -z ${TRAVIS_PULL_REQUEST} ]]; then
	use_git ${1}
else
	use_travis ${1}
fi
