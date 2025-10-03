pipeline {
	agent {
		label 's390x-operator-build'
	}
	parameters {
		string(name: 'BRANCH_NAME', defaultValue: 'main', description: 'Branch name to execute the image build from')
	}
	options {
		ansiColor('xterm')
		buildDiscarder(logRotator(numToKeepStr: '10'))
		disableConcurrentBuilds()
		timeout(time: 25, unit: 'HOURS')
		parallelsAlwaysFailFast()
	}
	environment {
		GH_CREDENTIALS=credentials('aiu.operator.github.api.credential')
		GOTOOLCHAIN = 'go1.24.4'
	}
	stages {
		stage('Checkout branch') {
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
					git branch --show-current
					git rev-parse --abbrev-ref HEAD
					echo BUILD_TYPE=$(./hack/get-build-type.bash)
					echo "git branch env var: ${GIT_BRANCH}"
					echo "change id env var : ${CHANGE_ID}"
					make echo-version
					git config --global --unset "url.https://taas-github-ibm-cache.swg-devops.com/.insteadof" || true
					git config --global url."https://x-access-token:${GH_CREDENTIALS_PSW}@github.ibm.com/".insteadOf "https://github.ibm.com/"
				'''
			}
		}
		stage('Login to docker registries') {
			environment {
				ICR_REGISTRY_CREDENTIALS = credentials('aiu.operator.icr.api.credential')
			}
			steps {
				sh '''
					#!/bin/bash -e
					podman logout icr.io/ibmaiu_internal || true
					rm -f /root/.docker/config.json || true
					echo "${ICR_REGISTRY_CREDENTIALS_PSW}" | podman login -u "${ICR_REGISTRY_CREDENTIALS_USR}" --password-stdin icr.io/ibmaiu_internal			
 				'''
			}
		}
		stage('Build & Push s390x Image') {
			steps {
				sh '''
					make docker-build-s390x docker-push-s390x
				'''
			}
		}
	}
	post {
		always {
			script {
				echo "Cleaning up workspace on s390x image builder ..."
				sh '''
				#!/bin/bash -e
				make docker-remove-images
				if pgrep -f "podman build" >/dev/null; then
					echo "WARNING: another build is in process, skipping cleanup"
				else
					podman images --all --filter "dangling=true" -q | xargs -r podman rmi -f || true
				fi
				'''
			}
		}
		cleanup {
			cleanWs disableDeferredWipeout: true, notFailBuild: true, cleanWhenNotBuilt: false, deleteDirs: true
		}
	}
}
