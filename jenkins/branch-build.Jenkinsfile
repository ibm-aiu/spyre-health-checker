// +-------------------------------------------------------------------+
// | Copyright IBM Corp. 2025 All Rights Reserved                      |
// +-------------------------------------------------------------------+

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
		GOPRIVATE = 'github.ibm.com/ai-chip-toolchain/*,github.ibm.com/ai-foundation/*'
		GOTOOLCHAIN='go1.24.13'
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
				echo "machine github.ibm.com login ${GH_CREDENTIALS_USR} password ${GH_CREDENTIALS_PSW}" > ${HOME}/.netrc
				'''
			}
		}
		stage ('Download dependencies') {
			steps {
				sh 'make ginkgo golangci-lint vendor'
				sh 'sudo apt-get update'
				sh 'sudo apt-get install -y bc unzip curl ca-certificates uuid-runtime'

			}
		}
		stage ('PR Build') {
			when {
				branch comparator: 'REGEXP', pattern: '^PR-\\d+$';
			}
			stages {
				stage('Run pre-commit check ') {
					steps {
						sh'''
						pip install --upgrade pip
						pip install pre-commit gitlint
						git config --unset-all core.hooksPath
						pre-commit install --install-hooks
						git fetch origin ${CHANGE_TARGET}:refs/remotes/origin/${CHANGE_TARGET}
						git fetch origin ${CHANGE_BRANCH}:refs/remotes/origin/${CHANGE_BRANCH}
						git log -1 --pretty=%B | gitlint
						pre-commit run --from-ref origin/${CHANGE_TARGET} --to-ref origin/${CHANGE_BRANCH}
						'''
					}
				}
				stage ('Run unit tests') {
					steps {
						sh 'make vendor fmt vet test'
					}
				}
				stage('Build executable and run linter') {
					steps {
						sh 'make vendor build lint'
					}
				}
				/* TODO Enable stage once a proper keys has been established
				stage('Run sonar qube scan for PR') {
					steps {
						script {
							jobResult = build(job: 'aiu-operator-pipelines/spyre-device-plugin-sonar-qube-scan',
										propagate:false,
										parameters: [
											string(name: 'BRANCH_NAME', value: "${env.CHANGE_BRANCH}"),
											string(name: 'SCAN_TYPE',   value: 'pr-scan'),
											string(name: 'PR', 			value: "${env.BRANCH_NAME}")
										]).result
							if (jobResult != 'SUCCESS') {
								catchError(buildResult: 'SUCCESS', stageResult: 'FAILURE') {
									warning('Sonar Qube scan failed...')
								}
							}
						}
					}
				}
				*/
			}
		}
		stage('Run detect-secrets') {
			when {
				not {
					branch comparator: 'REGEXP', pattern: '^PR-\\d+$';
				}
			}
			stages {
				stage('Run pre-commit check to detect-secrets ') {
					steps {
						sh'''
						pip install --upgrade pip
						pip install pre-commit
						git config --unset-all core.hooksPath
						pre-commit install --install-hooks
						pre-commit run detect-secrets --all-files
						'''
					}
				}
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
						ICR_REGISTRY_CREDENTIALS=credentials('aiu.operator.icr.api.credential')
					}
					steps {
						sh '''
						#!/bin/bash -e
						echo "${ICR_REGISTRY_CREDENTIALS_PSW}" | docker login -u "${ICR_REGISTRY_CREDENTIALS_USR}" --password-stdin icr.io/ibmaiu_internal
						'''
					}
				}
				stage ('Build images') {
					parallel {
						stage('Build amd64 images') {
							steps {
								sh '''
								make docker-build-amd64
								'''
							}
						}
						stage('Build s390x(IBM Z) images') {
							steps {
								script {
									build job: 'aiu-operator-pipelines/spyre-health-checker-image-s390x',
										parameters: [
											string(name: 'BRANCH_NAME', value: "${env.BRANCH_NAME}")
									]
								}
							}
						}
						stage('Build power images') {
							steps {
								script {
									build job: 'aiu-operator-pipelines/spyre-health-checker-image-build-power',
										parameters: [
											string(name: 'BRANCH_NAME', value: "${env.BRANCH_NAME}")
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
		stage('Twistlock') {
			when {
				not {
				   branch comparator: 'REGEXP', pattern: '^PR-\\d+$'
				}
			}
			stages {
				stage('Install Twistlock CLI') {
					steps {
						withCredentials([usernamePassword(credentialsId: 'aiu.operator.artifactory.api.credential', usernameVariable: 'ARTIFACTORY_USER', passwordVariable: 'ARTIFACTORY_PASS')]){
										 sh 'make tt-install ARTIFACTORY_USER="${ARTIFACTORY_USER}" ARTIFACTORY_PASS="${ARTIFACTORY_PASS}"'
						}
					}
				 }
				stage('Twistlock Scan') {
					stages {
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
							   withCredentials([usernamePassword(credentialsId: 'spyre.operator.twistlock.login.power', usernameVariable: 'TW_USER', passwordVariable: 'TW_PASS'),
									   string(credentialsId: 'spyre.operator.twistlock.apikey.power',variable: 'TWIST_LOCK_API_KEY'),
									string(credentialsId: 'spyre.operator.twistlock.controlgroup.power', variable: 'TT_CONTROL_GROUP')]) {
										sh '''
										make tt-scan-ppc64le \
										TT_USER="${TW_USER}:${TW_PASS}" \
										TT_CONTROL_GROUP="${TT_CONTROL_GROUP}" \
										TWIST_LOCK_API_KEY="${TWIST_LOCK_API_KEY}"
										'''
								}
							}
						}
					}
				}
				stage('Archive twistlock scan results') {
					steps {
						sh 'zip -r twistlock-scan-results.zip twistlock-scan-output/'
						archiveArtifacts artifacts: 'twistlock-scan-results.zip', fingerprint: true
					}
				}
			}
		}
		/* TODO: Enable stage once we have a proper key
		stage('Run SonarQube scan for default or release branch') {
			when {
				anyOf {
					branch comparator: 'REGEXP', pattern: '^main$';
					branch comparator: 'REGEXP', pattern: '^release_[0-9]+(\\_[0-9]+)+$';
					branch comparator: 'REGEXP', pattern: '^release_v[0-9]+(\\.[0-9]+)+$';
				}
			}
			steps {
				script {
					jobResult = build(job: 'aiu-operator-pipelines/spyre-device-plugin-sonar-qube-scan',
							propagate:false,
							parameters: [
								string(name: 'BRANCH_NAME', value: "${env.BRANCH_NAME}"),
								string(name: 'SCAN_TYPE',   value: "branch-scan")
							]).result
					if (jobResult != 'SUCCESS') {
						catchError(buildResult: 'SUCCESS', stageResult: 'FAILURE') {
							warning('Sonar Qube scan failed.')
						}
					}
				}
			}
		}
		*/
		/* TODO: enable e2e tests when the operator builds have been updated to
			accept the component
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
							]
					}
				}
				stage('Run e2e test for s390x') {
					steps {
						script {
							build job: 'aiu-operator-pipelines/aiu-operator-e2e-test-s390x',
								parameters: [
									string(name: 'BRANCH_NAME',     value: "${env.BRANCH_NAME}"),
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
								]
						}
					}
				}
			}
		}
		*/
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
						make github-release
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
		cleanup {
			cleanWs disableDeferredWipeout: true, notFailBuild: true, cleanWhenNotBuilt: false, deleteDirs: true
			sh 'rm -f ${HOME}/.netrc'
		}
	}
}
