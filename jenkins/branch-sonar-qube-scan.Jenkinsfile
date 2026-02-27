// +-------------------------------------------------------------------+
// | Copyright IBM Corp. 2025 All Rights Reserved                      |
// | PID 5698-SPR                                                      |
// +-------------------------------------------------------------------+

pipeline {
	agent {
		node {
			label 'SonarQube_scan'
			customWorkspace "./workspace/${JOB_NAME}/${BUILD_NUMBER}"
		}
	}
	parameters {
		string(name: 'BRANCH_NAME', defaultValue: 'main', description: 'Branch to scan')
		choice(name: 'SCAN_TYPE', choices: ['branch-scan', 'pr-scan'], description: 'The type of scan to perform')
		string(name: 'PR', defaultValue: '', description: 'The PR (CHANGE_ID) to run the scan for')
	}
	options {
		ansiColor('xterm')
		disableConcurrentBuilds()
		timeout(time: 25, unit: 'HOURS')
	}
	environment {
		GH_CREDENTIALS=credentials('aiu.operator.github.api.credential')
		SONARQUBE_API_TOKEN=credentials('wxpe-aiu.sonar.cube.api.key.credential')
		SONARQUBE_CERTS_PASSWORD=credentials('spyre.operator.sonar.qube.certs.password')
	}
	stages {

		stage('Checkout branch') {
			steps {
				sh "echo ${params.BRANCH_NAME}"
				sh "git checkout ${params.BRANCH_NAME}"
				sh "git status"
			}
		}
		stage ('Run sonar qube scan') {
			steps {
				sh "./jenkins/scripts/sonnar-qube.bash scan --${params.SCAN_TYPE} ${params.PR}"
			}
		}
	}
	post {
        cleanup {
            cleanWs disableDeferredWipeout: true, notFailBuild: true, cleanWhenNotBuilt: false, deleteDirs: true
        }
    }
}
