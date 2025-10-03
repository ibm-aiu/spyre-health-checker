pipeline {
	agent {
		label 'taas_image_with_docker_aiu_operator'
	}
	options {
		ansiColor('xterm')
		quietPeriod(60)
		buildDiscarder(logRotator(numToKeepStr: '10'))
		disableConcurrentBuilds(abortPrevious: true)
		timeout(time: 25, unit: 'HOURS')
	}
	environment {
		SLACK_INCOMING_WEBHOOK = credentials('aiu.operator.slack.api.credential')
		GH_CREDENTIALS=credentials('aiu.operator.github.api.credential')
		GOPRIVATE='github.ibm.com/ai-chip-toolchain/*'
		GOTOOLCHAIN='go1.24.4'
	}
	stages {
		stage('Checkout branch') {
			when {
				not {
					branch comparator: 'REGEXP', pattern: '^PR-\\d+$';
				}
			}
			steps {
				sh "git checkout ${env.GIT_BRANCH}"
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
				git config --global --unset "url.https://taas-github-ibm-cache.swg-devops.com/.insteadof" || true
				git config --global url."https://x-access-token:${GH_CREDENTIALS_PSW}@github.ibm.com/".insteadOf "https://github.ibm.com/"
				'''
			}
		}
		stage ('Download dependencies') {
			steps {
				sh 'make ginkgo golangci-lint vendor'
			}
		}
		stage ('Run unit tests') {
			steps {
				sh 'make test'
			}
		}
		stage('Build executable and run linter') {
			steps {
				sh 'make vendor build lint'
			}
		}
		stage('Build docker artifacts') {
			when {
				not {
					branch comparator: 'REGEXP', pattern: '^PR-\\d+$';
				}
			}
			stages {
				stage('Login to docker registries') {
					environment {
						RH_REGISTRY_CREDENTIALS = credentials('aiu.operator.redhat.registry.api.credential')
						ICR_REGISTRY_CREDENTIALS=credentials('aiu.operator.icr.api.credential')
					}
					steps {
						sh '''
						#!/bin/bash -e
						echo "${RH_REGISTRY_CREDENTIALS_PSW}" | docker login -u "${RH_REGISTRY_CREDENTIALS_USR}" --password-stdin registry.redhat.io
						echo "${ICR_REGISTRY_CREDENTIALS_PSW}" | docker login -u "${ICR_REGISTRY_CREDENTIALS_USR}" --password-stdin icr.io/ibmaiu_internal
						'''
					}
				}
				stage('Build images for each CPU arch') {
					parallel {
						stage('Build amd64 images') {
							steps {
								sh '''
								make docker-build-amd64 docker-push-amd64
								'''
							}
						}
						stage('Build s390x(IBM Z) images') {
							steps {
								script {
									build job: 'aiu-operator-pipelines/spyre-health-checker-image-s390x',
										parameters: [
											string(name: 'BRANCH_NAME',     value: "${env.BRANCH_NAME}")
									]
								}
							}
						}
						stage('Build power images') {
							steps {
								script {
									build job: 'aiu-operator-pipelines/spyre-health-checker-image-build-power',
										parameters: [
											string(name: 'BRANCH_NAME',     value: "${env.BRANCH_NAME}")
										]
								}
							}
						}
					}
				}
				stage('Collect images into a manifest') {
					steps {
						sh 'make docker-build-manifest docker-push-manifest'
					}
				}
			}
		}
		/* TODO: enable e2e tests when the operator builds have been updated to
		accept the component and also be upgraded to golang 1.24

		stage ('Run e2e test') {
			when {
				not {
					anyOf {
						branch comparator: 'REGEXP', pattern: '^PR-\\d+$';
						branch comparator: 'REGEXP', pattern: '^release_[0-9]+(\\_[0-9]+)+$';
						branch comparator: 'REGEXP', pattern: '^release_v[0-9]+(\\.[0-9]+)+$';
						branch comparator: 'REGEXP', pattern: '^v[0-9](\\.[0-9]+)+-rc\\.[0-9]+$';
					}
				}
			}
			parallel {
				stage('Run e2e test for amd64') {
					steps {
						build job: 'aiu-operator-pipelines/crc-aiu-operator-end-to-end-test',
							parameters: [
								string(name: 'BRANCH_NAME',     value: "${env.BRANCH_NAME}"),
								string(name: 'GOLANG_VERSION',  value: 'v1.23'),
							]
					}
				}
				stage('Run e2e test for s390x') {
					steps {
						script {
							build job: 'aiu-operator-pipelines/aiu-operator-e2e-test-s390x',
								parameters: [
									string(name: 'BRANCH_NAME',     value: "${env.BRANCH_NAME}"),
									string(name: 'GOLANG_VERSION',  value: 'v1.23'),
								]
						}
					}
				}
				stage('Run e2e test for power') {
					steps {
						script {
							build job: 'aiu-operator-pipelines/aiu-operator-e2e-test-ppc64le',
								parameters: [
									string(name: 'BRANCH_NAME',     value: "${env.BRANCH_NAME}"),
									string(name: 'GOLANG_VERSION',  value: 'v1.23'),
								]
						}
					}
				}
			}
		}
		*/
		stage('Twistlock') {
			when {
				not {
				   branch comparator: 'REGEXP', pattern: '^PR-\\d+$'
				}
			}
			stages {
				stage('Install Twistlock  Dependencies') {
					steps {
						sh '''
							sudo apt-get update
							sudo apt-get install -y unzip curl ca-certificates uuid-runtime
						'''
					}
			   	}
				stage('Install Twistlock CLI') {
					steps {
						withCredentials([string(credentialsId: 'aiu.operator.artifactory.bearer.token', variable: 'ARTIFACTORY_TOKEN')]) {
							sh 'make tt-install'
						}
					}
				 }
				 stage('Twistlock Scan') {
					 parallel {
						stage('Twistlock scan for amd64') {
							steps {
								/*
								withCredentials([usernamePassword(credentialsId: 'w3-twistlock-user-pass', usernameVariable: 'TW_USER', passwordVariable: 'TW_PASS'),
									string(credentialsId: 'twistlock-iam-api-key',variable: 'TWIST_LOCK_API_KEY')]) {
									sh '''
											make tt-scan-amd64 \
											TT_USER="${TW_USER}:${TW_PASS}" \
											TT_CONTROL_GROUP="${TT_CONTROL_GROUP}" \
											TWIST_LOCK_API_KEY="${TWIST_LOCK_API_KEY}"
										'''
								}
								*/
								echo "Scanning for this architecture is not enabled."
							}
						}
						stage('Twistlock scan for Scan s390x') {
							steps {
								withCredentials([usernamePassword(credentialsId: 'w3-twistlock-user-pass', usernameVariable: 'TW_USER', passwordVariable: 'TW_PASS'),
									string(credentialsId: 'twistlock-iam-api-key',variable: 'TWIST_LOCK_API_KEY'),
  									string(credentialsId: 'twistlock-control-group', variable: 'TT_CONTROL_GROUP')]) {
										sh '''  
										make tt-scan-s390x  TT_USER="${TW_USER}:${TW_PASS}" TT_CONTROL_GROUP="${TT_CONTROL_GROUP}" TWIST_LOCK_API_KEY="${TWIST_LOCK_API_KEY}"
										'''
								}
							}
						}
						stage('Twistlock scan for Scan ppc64le') {
						   steps {
								/*
							   withCredentials([usernamePassword(credentialsId: 'w3-twistlock-user-pass', usernameVariable: 'TW_USER', passwordVariable: 'TW_PASS'),
							   string(credentialsId: 'twistlock-iam-api-key',variable: 'TWIST_LOCK_API_KEY') ]) {					                                         
								sh ''' 
									   make tt-scan-power \
									   TT_USER="${TW_USER}:${TW_PASS}" \
									   TT_CONTROL_GROUP="${TT_CONTROL_GROUP}" \
									   TWIST_LOCK_API_KEY="${TWIST_LOCK_API_KEY}"
									'''
								}
								*/
								echo "Scanning for this architecture is not enabled."
							}  
						}
					}
				}
			}
		}
		stage('Create GH release') {
			when {
				anyOf {
					branch comparator: 'REGEXP', pattern: '^release_[0-9]+(\\_[0-9]+)+$';
					branch comparator: 'REGEXP', pattern: '^release_v[0-9]+(\\.[0-9]+)+$';
					branch comparator: 'REGEXP', pattern: '^v[0-9](\\.[0-9]+)+-rc\\.[0-9]+$';
				}
			}
			stages {
				stage('Add GH CLI') {
					steps {
							sh'''
							#!/bin/bash -e
							(type -p wget >/dev/null || (sudo apt update && sudo apt-get install wget -y)) \
								&& sudo mkdir -p -m 755 /etc/apt/keyrings \
								&& wget -qO- https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null \
								&& sudo chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
								&& echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
								&& sudo apt update \
								&& sudo apt install gh -y
							'''
						}
				}
				stage ('Create release') {
					steps {
						sh'''
						#!/bin/bash -e
						tmpfile=$(mktemp /tmp/git.XXXXXX)
						export GIT_ASKPASS="$tmpfile"
						export GITHUB_TOKEN=${GH_CREDENTIALS_PSW}
						trap 'rm -f $GIT_ASKPASS' EXIT
						echo "echo $GITHUB_TOKEN" >> "$tmpfile"
						chmod +x "$tmpfile"
						git config --global --unset "url.https://taas-github-ibm-cache.swg-devops.com/.insteadof" || true
						echo "$GITHUB_TOKEN" | gh auth login --hostname github.ibm.com --with-token
						make release-tag-push
						make create-gh-release
						'''
					}
				}
			}
		}
	}
	post {
		always {
			script {
				env.CURRENT_BUILD_RESULT = currentBuild.currentResult
				sh'''
					#!/bin/bash -e
					pip3 install requests
					python3 hack/slack-notifier.py --job_status "${CURRENT_BUILD_RESULT}" --job_name "${JOB_NAME}" --build_url "${BUILD_URL}"
				'''
			}
			sleep(2)
		}
	}
}
