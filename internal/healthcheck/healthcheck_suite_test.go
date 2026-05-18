/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
)

func TestHealthCheck(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Spyre Health Checker Test HealthCheck Suite")
}

var _ = BeforeSuite(func() {
	Expect(os.Setenv(utils.PseudoDeviceModeKey, "1")).To(Succeed())

	log.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
})

var _ = AfterSuite(func() {
	Expect(os.Unsetenv(utils.PseudoDeviceModeKey)).To(Succeed())
})
