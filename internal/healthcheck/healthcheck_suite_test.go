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
	os.Setenv(utils.PseudoDeviceModeKey, "1")

	log.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
})

var _ = AfterSuite(func() {
	os.Unsetenv(utils.PseudoDeviceModeKey)
})
