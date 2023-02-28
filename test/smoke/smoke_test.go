package smoke

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/diagridio/dapr-cert-manager/test/smoke/config"
)

var (
	cnf *config.Config
)

func init() {
	// subtle: Flags need to be registered in an init function when Ginkgo is used.
	// If not, go test will call flag.Parse before ginkgo runs and our custom args will
	// not be respected
	cnf = config.New(flag.CommandLine)

	wait.ForeverTestTimeout = time.Second * 60
}

var _ = BeforeSuite(func() {
	Expect(cnf.Complete()).NotTo(HaveOccurred())
})

func Test_Smoke(t *testing.T) {
	runSuite(t, "smoke-dapr-cert-manager", "../../_artifacts")
}

func runSuite(t *testing.T, suiteName, artifactDir string) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	if err := os.MkdirAll(artifactDir, 0o775); err != nil {
		t.Fatalf("failed to ensure artifactDir %q exists to store reports in: %s", artifactDir, err.Error())
	}

	junitDestination := filepath.Join(artifactDir, fmt.Sprintf("junit-go-%s.xml", suiteName))

	suiteConfig, reporterConfig := ginkgo.GinkgoConfiguration()

	reporterConfig.JUnitReport = junitDestination

	suiteConfig.RandomizeAllSpecs = true

	ginkgo.RunSpecs(t, suiteName, suiteConfig, reporterConfig)
}
