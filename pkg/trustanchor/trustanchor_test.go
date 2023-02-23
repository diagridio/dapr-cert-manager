package trustanchor

import (
	"testing"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func Test_trustanchor_x509bundleSource(t *testing.T) {
	var _ x509bundle.Source = New(Options{Log: klogr.New()})
	var _ manager.LeaderElectionRunnable = New(Options{Log: klogr.New()})
}
