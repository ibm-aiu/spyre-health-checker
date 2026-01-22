#!/bin/bash
# +-------------------------------------------------------------------+
# | Copyright IBM Corp. 2025 All Rights Reserved                      |
# | PID 5698-SPR                                                      |
# +-------------------------------------------------------------------+

set -eu -o pipefail
readonly GIT_PULL_BASE="main"
readonly GIT_BRANCH_NAME=$(git branch --show-current)
SONARQUBE_SCANNER="${SONARQUBE_SCANNER:-""}"
SONARQUBE_API_TOKEN="${SONARQUBE_API_TOKEN:-""}"
SONARQUBE_CERTS="${SONARQUBE_CERTS:=-""}"
SONARQUBE_GIT_PULL_NUMBER="${SONARQUBE_GIT_PULL_NUMBER:-""}"
SONARQUBE_CERTS_PASSWORD="${SONARQUBE_CERTS_PASSWORD:-""}"
SCAN_TYPE="${SCAN_TYPE:-""}"

function usage() {
	echo "Usage: ${0} flags"
	echo "Arguments:"
	echo "   scan   -b|--branch-scan        	run a branch scan"
	echo "   scan   -p|--pr-scan     <PR ID> 	run a pr scan"
	echo "  -h, --help prints this message"
	echo " The following environment variables need to be defined: "
	echo "  SONNARQUBE_API_TOKEN -- the api key                    "
	echo "  SONARQUBE_CERTS_PASSWORD -- certificate password       "
	echo "	SONARQUBE_CERTS -- path to sonar qube certificates	   "
	exit 2
}
function validate_environment() {
	if [[ "x${SONARQUBE_API_TOKEN}" == "x" ]]; then
		echo "SONARQUBE_API_TOKEN not defined"
		exit 1
	fi

	if [[ "x${SONARQUBE_CERTS}" == "x" ]]; then
		echo "SONARQUBE_CERTS not defined"
		exit 1
	fi

	if [[ "x${SONARQUBE_CERTS_PASSWORD}" == "x" ]]; then
		echo "SONARQUBE_CERTS_PASSWORD not defined"
		exit 1
	fi

	if [[ "x${SONARQUBE_GIT_PULL_NUMBER}" == "x" && ${SCAN_TYPE} == "pr" ]]; then
		echo "SONARQUBE_GIT_PULL_NUMBER not defined for a PR scan type"
		exit 1
	fi

	if [[ "x${SONARQUBE_SCANNER}" == "x" ]]; then
		echo "SONARQUBE_SCANNER not defined"
		exit 1
	fi

	if [[ "x${SCAN_TYPE}" == "x" ]]; then
		echo "SCAN_TYPE must be set"
		exit 1
	fi

}

# Check if at least one argument is provided
if [ $# -eq 0 ]; then
	usage
	exit 1
fi

# Get the command from the first argument
command=$1
shift

# Process long and short arguments
while [[ $# -gt 0 ]]; do
	case $1 in
	-b | --branch-scan)
		SCAN_TYPE="branch"
		;;
	-p | --pr-scan)
		SCAN_TYPE="pr"
		SONARQUBE_GIT_PULL_NUMBER=${2}
		shift
		;;
	*)
		echo "Unknown argument: $1"
		usage
		;;
	esac
	shift
done

echo "Running scan of type '${SCAN_TYPE}'"
validate_environment
case ${SCAN_TYPE} in
branch)
	${SONARQUBE_SCANNER} \
		-D sonar.sources=. \
		-D sonar.token=${SONARQUBE_API_TOKEN} \
		-D sonar.branch.name=${GIT_BRANCH_NAME} \
		-D sonar.scanner.truststorePath=${SONARQUBE_CERTS}/castorevpcprod \
		-D sonar.scanner.truststorePassword=${SONARQUBE_CERTS_PASSWORD}
	;;
pr)
	echo "SONARQUBE_GIT_PULL_NUMBER='${SONARQUBE_GIT_PULL_NUMBER}'"
	${SONARQUBE_SCANNER} \
		-D sonar.sources=. \
		-D sonar.token=${SONARQUBE_API_TOKEN} \
		-D sonar.pullrequest.key="${SONARQUBE_GIT_PULL_NUMBER}" \
		-D sonar.pullrequest.branch="${GIT_BRANCH_NAME}" \
		-D sonar.pullrequest.base="${GIT_PULL_BASE}" \
		-D sonar.scanner.truststorePath=${SONARQUBE_CERTS}/castorevpcprod \
		-D sonar.scanner.truststorePassword=${SONARQUBE_CERTS_PASSWORD}
	;;
*)
	echo "Unknown scan type: ${SCAN_TYPE}"
	usage
	;;
esac
