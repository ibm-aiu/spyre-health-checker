// +-------------------------------------------------------------------+
// | Copyright IBM Corp. 2025 All Rights Reserved                      |
// +-------------------------------------------------------------------+

pipeline {
	agent {
		node {
			label 'secure-build-power'
			customWorkspace "./workspace/${JOB_NAME}/${BUILD_NUMBER}"
		}
	}
	parameters {
		string(name: 'BRANCH_NAME', defaultValue: 'main', description: 'Branch name to execute the image build from')
	}
	options {
		ansiColor('xterm')
		buildDiscarder(logRotator(numToKeepStr: '10'))
		disableConcurrentBuilds()
		timeout(time: 25, unit: 'HOURS')
	}
	environment {
		GH_CREDENTIALS=credentials('aiu.operator.github.api.credential')
		PATH = "${env.PATH}:/var/jenkins-home/go/bin"
		GOPRIVATE = 'github.ibm.com/ai-chip-toolchain/*,github.ibm.com/ai-foundation/*'
		GOTOOLCHAIN = 'go1.24.13'
		DOCKER = "podman"
	}
	stages {
		stage('Checkout branch') {
			when {
				not {
					branch comparator: 'REGEXP', pattern: '^PR-\\d+$';
				}
			}
			steps {
				sh "echo ${params.BRANCH_NAME}"
				sh "git checkout ${params.BRANCH_NAME}"
				sh "git status"
			}
		}
		stage('Echo environment and reset git config') {
			steps {
				sh'''
				#!/bin/bash -e
				go version
				git branch --show-current
				git rev-parse --abbrev-ref HEAD
				echo BUILD_TYPE=$(./hack/get-build-type.bash)
				echo "git branch env var: ${GIT_BRANCH}"
				echo "change id env var : ${CHANGE_ID}"
				make echo-version
				'''
			}
		}
		stage('Login to docker registries') {
			environment {
				ICR_REGISTRY_CREDENTIALS=credentials('aiu.operator.icr.api.credential')
			}
			steps {
				sh '''
				#!/bin/bash -e
				echo "${ICR_REGISTRY_CREDENTIALS_PSW}" | ${DOCKER} login -u "${ICR_REGISTRY_CREDENTIALS_USR}" --password-stdin icr.io/ibmaiu_internal
				'''
			}
		}
		stage('Build & Push Power Image') {
			steps {
				sh '''
				#!/bin/bash -e
				export GIT_ASKPASS="${PWD}/jenkins/scripts/git-askpass.bash"
				export GIT_TERMINAL_PROMPT=1
				echo ${GIT_ASKPASS}
				make docker-build-power docker-push-power
				'''
			}
		}
	}
	post {
		always {
			script {
				echo "Cleaning up workspace on Power Image Builder ..."
				sh '''
				#!/bin/bash -e
				make docker-remove-images
				if pgrep -f "${DOCKER} build" >/dev/null; then
					echo "WARNING: another build is in process, skipping cleanup"
				else
					${DOCKER} images --all --filter "dangling=true" -q | xargs -r ${DOCKER} rmi -f || true
				fi
				'''
			}
		}
		cleanup {
			cleanWs disableDeferredWipeout: true, notFailBuild: true, cleanWhenNotBuilt: false, deleteDirs: true
		}
	}
}
