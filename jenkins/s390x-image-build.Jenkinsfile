pipeline {
    agent {
        node {
            label 'z-native'
        }
    }
    parameters {
        string(name: 'BRANCH_NAME', defaultValue: 'main', description: 'Branch name to execute the image build from')
        string(name: 'IBM_CLOUD_HOST', defaultValue: '158.176.7.10', description: 'IBM Cloud VM hostname')
    }
    options {
        ansiColor('xterm')
        buildDiscarder(logRotator(numToKeepStr: '10'))
        disableConcurrentBuilds()
        timeout(time: 25, unit: 'HOURS')
        parallelsAlwaysFailFast()
    }
    environment {
        JUMP_HOST   = credentials('Z-jump-host-ip')
        TARGET_HOST = credentials('Z-target-host-ip')
        UNIQUE_WORKSPACE = "spyre-operator-${BUILD_ID}"
        REPO_DIR = "/root/spyre-operator/${UNIQUE_WORKSPACE}/spyre-health-checker"
    }
    stages {
        stage('Define sshRun helper') {
            steps {
                script {
                    sshRun = { cmd ->
                        sh """
                            ssh -A -J ocpai@${env.JUMP_HOST} root@${env.TARGET_HOST} '${cmd}'
                        """
                    }
                }
            }
        }
        stage('Validate SSH Connection') {
            steps {
                script {
                    echo "SSH Connection Successful to TARGET_HOST=${env.TARGET_HOST} via JUMP_HOST=${env.JUMP_HOST}"
                }
            }
        }
        stage('Prepare Build Environment') {
            steps {
                script {
                    withCredentials([
                        usernamePassword(credentialsId: 'aiu.operator.github.api.credential',
                                         usernameVariable: 'GH_USER',
                                         passwordVariable: 'GH_PASS')
                    ]) {
                        def encodedUser = java.net.URLEncoder.encode(GH_USER, "UTF-8")
                        def encodedPass = java.net.URLEncoder.encode(GH_PASS, "UTF-8")

                        sshRun("""
                            # Remove existing git config file if it exists
                            rm -f /root/.gitconfig || true && \

                            # Create build directory
                            mkdir -p /root/spyre-operator/${UNIQUE_WORKSPACE} && \
                            cd /root/spyre-operator/${UNIQUE_WORKSPACE} && \

                            # Configure git with proper escaping
                            git config --global --unset-all "url.https://taas-github-ibm-cache.swg-devops.com/.insteadof" || true && \
                            git config --global 'url."https://x-access-token:'"${GH_PASS}"'@github.ibm.com/".insteadOf' "https://github.ibm.com/" && \

                            # Set GOPRIVATE and validate git config
                            export GOPRIVATE=github.ibm.com && \
                            git config --global --list && \

                            # Clone the repository
                            git clone --branch ${params.BRANCH_NAME} https://x-access-token:${GH_PASS}@github.ibm.com/ai-chip-toolchain/spyre-health-checker.git
                        """)
                    }
                }
            }
        }
        stage('Checkout Branch') {
            steps {
                script {
                    sshRun("""
                        cd ${REPO_DIR} && \
                        git checkout ${params.BRANCH_NAME} && \
                        git status
                    """)
                }
            }
        }
        stage('Echo Build Info') {
            steps {
                script {
                    def changeIdValue = env.CHANGE_ID ?: 'N/A'
                    sshRun("""
                        cd ${REPO_DIR} && \
                        echo "Branch: \$(git branch --show-current)" && \
                        echo "Build Type: \$(./hack/get-build-type.bash)" && \
                        echo "GIT_BRANCH: ${env.GIT_BRANCH}" && \
                        echo "CHANGE_ID: ${changeIdValue}" && \
                        make echo-version
                    """)
                }
            }
        }
        stage('Login to ICR via Podman') {
            steps {
                script {
                    withCredentials([
                        usernamePassword(credentialsId: 'aiu.operator.icr.api.credential',
                                         usernameVariable: 'ICR_USER',
                                         passwordVariable: 'ICR_PASS')
                    ]) {
                        sshRun("""
                        export ICR_USER='${ICR_USER}' && \
                        export ICR_PASS='${ICR_PASS}' && \
                        podman logout icr.io/ibmaiu_internal || true && \
                        rm -f /root/.docker/config.json || true && \
                        echo "\$ICR_PASS" | podman login -u "\$ICR_USER" --password-stdin icr.io/ibmaiu_internal
                        """)
                    }
                }
            }
        }
        stage('Build & Push s390x Image') {
            steps {
                script {
                    sshRun("""
                       echo "Verifying Go installation:" && \
                	   go version && \
               		   echo "Starting build process:" && \
                	   cd ${REPO_DIR} && \
                	   go mod vendor && \
                	   make docker-build-s390x docker-push-s390x
		            """)
                }
            }
        }
    }
    post {
        always {
            script {
                echo "Cleaning up workspace"
                sshRun("""
                    make docker-remove-images && \
                    if pgrep -f "podman build" >/dev/null; then echo "WARNING: another build is in process, skipping cleanup"; else podman images --all --filter "dangling=true" -q | xargs -r podman rmi -f || true; fi && \
                    rm -rf /root/spyre-operator/${UNIQUE_WORKSPACE} || true
                """)
            }
        }
    }
}
