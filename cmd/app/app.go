package app

import (
	"fmt"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/spf13/cobra"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/diagridio/dapr-cert-manager-helper/cmd/app/options"
	"github.com/diagridio/dapr-cert-manager-helper/pkg/controller"
	"github.com/diagridio/dapr-cert-manager-helper/pkg/trustanchor"
)

const (
	helpOutput = "Operator for managing a dapr trust bundle using a cert-manager Certificate resource"
)

// NewCommand will return a new command instance for the
// dapr-cert-manager-helper operator.
func NewCommand() *cobra.Command {
	opts := options.New()

	cmd := &cobra.Command{
		Use:   "dapr-cert-manager-helper",
		Short: helpOutput,
		Long:  helpOutput,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete(); err != nil {
				return err
			}

			cl, err := kubernetes.NewForConfig(opts.RestConfig)
			if err != nil {
				return fmt.Errorf("error creating kubernetes client: %s", err.Error())
			}

			mlog := opts.Logr.WithName("manager")
			eventBroadcaster := record.NewBroadcaster()
			eventBroadcaster.StartLogging(func(format string, args ...interface{}) { mlog.V(3).Info(fmt.Sprintf(format, args...)) })
			eventBroadcaster.StartRecordingToSink(&clientv1.EventSinkImpl{Interface: cl.CoreV1().Events("")})

			scheme := runtime.NewScheme()
			if err := cmapi.AddToScheme(scheme); err != nil {
				return fmt.Errorf("error adding cert-manager scheme: %w", err)
			}

			mgr, err := ctrl.NewManager(opts.RestConfig, ctrl.Options{
				Scheme:                        scheme,
				EventBroadcaster:              eventBroadcaster,
				LeaderElection:                true,
				LeaderElectionNamespace:       opts.DaprNamespace,
				LeaderElectionID:              "dapr-cert-manager-helper",
				LeaderElectionReleaseOnCancel: true,
				ReadinessEndpointName:         "/readyz",
				HealthProbeBindAddress:        fmt.Sprintf("0.0.0.0:%d", opts.ReadyzPort),
				MetricsBindAddress:            fmt.Sprintf("0.0.0.0:%d", opts.MetricsPort),
				Logger:                        mlog,
				Namespace:                     opts.DaprNamespace,
				LeaderElectionResourceLock:    "leases",
			})
			if err != nil {
				return fmt.Errorf("failed to create manager: %w", err)
			}

			ctx := ctrl.SetupSignalHandler()
			var taSource x509bundle.Source
			if len(opts.TrustAnchorFileName) > 0 {
				ta := trustanchor.New(trustanchor.Options{
					Log:             opts.Logr,
					TrustBundlePath: opts.TrustAnchorFileName,
				})
				if err := mgr.Add(ta); err != nil {
					return err
				}
				taSource = ta
			}

			if err := controller.AddTrustBundle(ctx, mgr, controller.Options{
				Log:                        opts.Logr,
				DaprNamespace:              opts.DaprNamespace,
				TrustBundleCertificateName: opts.TrustBundleCertificateName,
				TrustAnchor:                taSource,
			}); err != nil {
				return err
			}

			// Start all runnables and controller
			return mgr.Start(ctx)
		},
	}

	opts = opts.Prepare(cmd)

	return cmd
}
