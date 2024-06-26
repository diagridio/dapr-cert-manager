package app

import (
	"errors"
	"fmt"
	"net/http"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/diagridio/dapr-cert-manager/cmd/app/options"
	"github.com/diagridio/dapr-cert-manager/pkg/controller"
	"github.com/diagridio/dapr-cert-manager/pkg/trustanchor"
)

const (
	helpOutput = "Operator for managing a dapr trust bundle using a cert-manager Certificate resource"
)

// NewCommand will return a new command instance for the
// dapr-cert-manager operator.
func NewCommand() *cobra.Command {
	opts := options.New()

	cmd := &cobra.Command{
		Use:   "dapr-cert-manager",
		Short: helpOutput,
		Long:  helpOutput,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete(); err != nil {
				return err
			}

			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				return fmt.Errorf("error adding corev1 to scheme: %w", err)
			}
			if err := cmapi.AddToScheme(scheme); err != nil {
				return fmt.Errorf("error adding cert-manager scheme: %w", err)
			}

			cl, err := kubernetes.NewForConfig(opts.RestConfig)
			if err != nil {
				return fmt.Errorf("error creating kubernetes client: %s", err.Error())
			}

			mlog := opts.Logr.WithName("manager")
			eventBroadcaster := record.NewBroadcaster()
			eventBroadcaster.StartLogging(func(format string, args ...any) { mlog.V(3).Info(fmt.Sprintf(format, args...)) })
			eventBroadcaster.StartRecordingToSink(&clientv1.EventSinkImpl{Interface: cl.CoreV1().Events("")})

			mgr, err := ctrl.NewManager(opts.RestConfig, ctrl.Options{
				Scheme:                        scheme,
				EventBroadcaster:              eventBroadcaster,
				LeaderElection:                true,
				LeaderElectionNamespace:       opts.DaprNamespace,
				LeaderElectionID:              "dapr-cert-manager",
				LeaderElectionReleaseOnCancel: true,
				ReadinessEndpointName:         "/readyz",
				HealthProbeBindAddress:        fmt.Sprintf(":%d", opts.ReadyzPort),
				Metrics:                       server.Options{BindAddress: fmt.Sprintf(":%d", opts.MetricsPort)},
				Logger:                        mlog,
				NewCache: func(config *rest.Config, o cache.Options) (cache.Cache, error) {
					o.DefaultNamespaces = map[string]cache.Config{opts.DaprNamespace: {}}
					return cache.New(config, o)
				},
				LeaderElectionResourceLock: "leases",
			})
			if err != nil {
				return fmt.Errorf("failed to create manager: %w", err)
			}

			// Add readiness check that the manager's informers have been synced.
			mgr.AddReadyzCheck("informers_synced", func(req *http.Request) error {
				if mgr.GetCache().WaitForCacheSync(req.Context()) {
					return nil
				}
				return errors.New("informers not synced")
			})

			ctx := ctrl.SetupSignalHandler()
			var taSource trustanchor.Interface
			if len(opts.TrustAnchorFilePath) > 0 {
				taSource = trustanchor.New(trustanchor.Options{
					Log:             opts.Logr,
					TrustBundlePath: opts.TrustAnchorFilePath,
				})
				if err := mgr.Add(taSource); err != nil {
					return err
				}
			}

			if err := controller.AddTrustBundle(mgr, controller.Options{
				Log:                            opts.Logr,
				DaprNamespace:                  opts.DaprNamespace,
				TrustBundleCertificateName:     opts.TrustBundleCertificateName,
				TrustAnchor:                    taSource,
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
