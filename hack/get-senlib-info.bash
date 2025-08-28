#!/bin/bash
# Copyright (c) 2022 IBM Corp. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -e

function usage() {
	cat <<EOF
    get-senlib-info.sh target arch image

    Gets target information from the given architecture and senlib image.

    Examples:
      get-senlib-info.bash toolbox-bin amd64 icr.io/ibmaiu_internal/x86_64/dd2/e2e_stable:latest
      get-senlib-info.bash rpm-suffix amd64 icr.io/ibmaiu_internal/x86_64/dd2/e2e_stable:latest

    Note: "DOCKER" env must be set.

    target - Target options: rpm-suffix, toolbox-bin, python-version
    arch   - Target linux architecture e.g., amd64, ppc64le, s390x
    image  - Target senlib image e.g., e2e_stable:latest
EOF
	exit 2
}

function get_python_version_patch() {
	local arch=${1}
	local image=${2}
	${DOCKER} run --rm --platform linux/${arch} ${image} python3 --version
}

function get_python_version() {
	local arch=${1}
	local image=${2}
	patch_version=$(get_python_version_patch ${arch} ${image})
	echo ${patch_version} | awk '{split($2, v, "."); print v[1]"."v[2]}'
}

function get_rpm_file() {
	local arch=${1}
	local image=${2}
	${DOCKER} run --rm --platform linux/${arch} \
		-e arch=$arch ${image} \
		bash -c "ls /project_package/ibm-senlib-core* | head -n 1"
}

function get_rpm_suffix() {
	local arch=${1}
	local image=${2}
	rpm_file=$(get_rpm_file ${arch} ${image})
	echo ${rpm_file:33} # cut /project_package/ibm-senlib-core-
}

function get_tool_bin() {
	local arch=${1}
	local image=${2}
	${DOCKER} run --rm --platform linux/${arch} \
		-e arch=$arch ${image} \
		bash -c "find /opt -name aiu-discover-topo 2>/dev/null | head -n 1 | xargs dirname"
}

function get_promclient_path() {
	local arch=${1}
	local image=${2}
	${DOCKER} run --rm --platform linux/${arch} ${image} bash -c "find /opt -name promclient.py 2>/dev/null | head -n 1"
}

function get_install_path() {
	local arch=${1}
	local image=${2}
	promclient_path=$(get_promclient_path ${arch} ${image})
	dirname "$(dirname "$(dirname "$(dirname ${promclient_path}")")")")"
}

function get_libhwloc_name() {
	local arch=${1}
	local image=${2}
	${DOCKER} run --rm --platform linux/${arch} \
		-e arch=$arch ${image} \
		bash -c "find /lib64/ -type f -name libhwloc.so.*"
}

if [[ $# -ne 3 ]]; then
	echo "Invalid number of elements in version. Expecting 3"
	usage
	exit 1
fi

case $1 in
python-version)
	get_python_version $2 $3
	;;
rpm-suffix)
	get_rpm_suffix $2 $3
	;;
toolbox-bin)
	get_tool_bin $2 $3
	;;
install-path)
	get_install_path $2 $3
	;;
libhwloc-name)
	get_libhwloc_name $2 $3
	;;
*)
	echo "Unknown command line argument: '${1}'"
	usage
	;;
esac
