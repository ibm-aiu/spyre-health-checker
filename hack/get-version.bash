#!/bin/bash
# Copyright 2024.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
set -e
function usage() {
	echo "Usage:   get-version.sh current-version"
	exit 2
}
function branch_name_check() {
	local branch_name=${1}
	local current_version=${2}
	local hash=${3}
	if [[ ${branch_name} =~ ^release_[0-9]+(\_[0-9]+)+$ ||
		${branch_name} =~ ^release_v[0-9]+(\.[0-9]+)+$ ||
		${branch_name} =~ ^v[0-9](\.[0-9]+)+-rc\.[0-9]+$ ]]; then
		echo ${current_version}
	elif [[ ${branch_name} =~ ^v[0-9]+\.[0-9]+$ ||
		${branch_name} == "main" ]]; then
		echo ${current_version}-dev
	else
		echo ${current_version}-dev-${hash}
	fi
}
function use_git() {
	local current_version=${1}
	local short_hash=$(git rev-parse --short=7 HEAD)
	local branch_name=$(git branch --show-current)
	if [[ -z ${branch_name} ]]; then
		branch_name=$(git rev-parse --abbrev-ref HEAD)
	fi
	branch_name_check ${branch_name} ${current_version} ${short_hash}
}
function use_travis() {
	local current_version=${1}
	local short_hash=${TRAVIS_COMMIT::7} # use only the first 7 characters from the variable
	local branch_name=${TRAVIS_BRANCH}
	if [[ ${TRAVIS_PULL_REQUEST} == "false" ]]; then
		branch_name_check ${branch_name} ${current_version} ${short_hash}
		return
	fi
	if [[ ${TRAVIS_PULL_REQUEST} != "false" ]]; then
		# Use the PR branch name, as the PR is against the main
		branch_name=${TRAVIS_PULL_REQUEST_BRANCH}
		branch_name_check ${branch_name} ${current_version} ${short_hash}
	fi
}

if [[ $1 == "" ]]; then
	usage
fi

# if TRAVIS_PULL_REQUEST environment variable is not present
if [[ -z ${TRAVIS_PULL_REQUEST} ]]; then
	use_git ${1}
else
	use_travis ${1}
fi
