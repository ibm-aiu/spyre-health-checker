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

set -eu
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
readonly REPO_ROOT=${SCRIPT_DIR%/*}
readonly CURRENT_VERSION=$(cat ${REPO_ROOT}/VERSION)

LAST_RC_TAG=$(git ls-remote --quiet --exit-code --tags --sort="-version:refname" | grep -E "refs/tags/v${CURRENT_VERSION}-rc\.[0-9]+$" | head -n 1 | awk '{print $2}')
if [ -z ${LAST_RC_TAG} ]; then
	echo "0"
	exit 0
fi

declare -a VA
VA=($(echo $LAST_RC_TAG | sed -r 's/(refs\/tags\/v)|(\.)|(-rc.)/ /g'))

if [[ ${#VA[@]} -eq 3 ]]; then
	echo "0"
elif [[ ${#VA[@]} -eq 4 ]]; then
	echo "${VA[3]}"
else
	>&2 echo "Invalid number of elements in tag (${LAST_RC_TAG}). Expecting either 3 or 4, found ${#VA[@]}"
	exit 1
fi
