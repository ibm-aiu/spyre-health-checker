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
        parallelsAlwaysFailFast()
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
                stage('Run docker image build') {
                    steps {
                        script {
                            BUILD_TYPE=sh(returnStdout: true, script: './hack/get-build-type.bash').trim()
                            env.DOCKER_GO_BUILD_FLAGS= "-p 4"
                            sh '''
                            #!/bin/bash -e
                            git config --global --unset "url.https://taas-github-ibm-cache.swg-devops.com/.insteadof" || true
                            git config --global url."https://x-access-token:${GH_CREDENTIALS_PSW}@github.ibm.com/".insteadOf "https://github.ibm.com/"
                            '''
                            if (BUILD_TYPE == "pr") {
                                // for PR build types only build the amd64 image
                                sh '''
                                #!/bin/bash -e
                                make print-DOCKER_BUILD_OPTS
                                make docker-build-push
                                '''
                            } else {
                                // all other build types build the multi-arch image
                                sh '''
                                #!/bin/bash -e
                                make print-DOCKER_BUILD_OPTS
                                make docker-build-pushx
                                '''
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
