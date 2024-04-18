package options

import (
	"flag"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
)

// Options is a struct to hold options for dapr-cert-manager.
type Options struct {
	logLevel        string
	kubeConfigFlags *genericclioptions.ConfigFlags

	// ReadyzPort is the TCP port used to expose the readiness probe on 0.0.0.0
	// on the path `/readyz`.
	ReadyzPort int

	// MetricsPort is the TCP port for exposing Prometheus metrics on 0.0.0.0 on the
	// path '/metrics'.
	MetricsPort int

	// Logr is the shared base logger.
	Logr logr.Logger

	// RestConfig is the shared based rest config to connect to the Kubernetes
	// API.
	RestConfig *rest.Config

	// DaprNamespace is the namespace where Dapr is installed.
	DaprNamespace string

	// TrustBundleCertificateName is the name of the cert-manager Certificate
	// which signs and manages the dapr trust bundle.
	TrustBundleCertificateName string

	// TrustAnchorFilePath is the name of the file which contains the trust
	// anchor for all 3 root CAs.
	// If empty, the trust anchor will be sourced from the cert-manager
	// Certificate.
	TrustAnchorFilePath string
}

// New constructs a new Options.
func New() *Options {
	return new(Options)
}

// Prepare adds Options flags to the CLI command.
func (o *Options) Prepare(cmd *cobra.Command) *Options {
	o.addFlags(cmd)
	return o
}

// Complete will populate the remaining Options from the CLI flags. Must be run
// before consuming Options.
func (o *Options) Complete() error {
	klog.InitFlags(nil)
	log := klogr.New()
	flag.Set("v", o.logLevel)
	o.Logr = log.WithName("dapr-cert-manager")

	var err error
	o.RestConfig, err = o.kubeConfigFlags.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to build kubernetes rest config: %s", err)
	}

	if len(o.DaprNamespace) == 0 {
		return fmt.Errorf("--dapr-namespace must be set")
	}

	if len(o.TrustAnchorFilePath) > 0 {
		if _, err := os.Stat(o.TrustAnchorFilePath); err != nil {
			return fmt.Errorf("failed to get trust anchor file %q: %w", o.TrustAnchorFilePath, err)
		}
		log.Info("using trust anchor from file", "file", o.TrustAnchorFilePath)
	} else {
		log.Info("trust anchor file name not set, will use cert-manager Certificate")
	}

	return nil
}

// addFlags add all Options flags to the given command.
func (o *Options) addFlags(cmd *cobra.Command) {
	var nfs cliflag.NamedFlagSets

	o.addAppFlags(nfs.FlagSet("App"))
	o.kubeConfigFlags = genericclioptions.NewConfigFlags(true)
	o.kubeConfigFlags.AddFlags(nfs.FlagSet("Kubernetes"))

	usageFmt := "Usage:\n  %s\n"
	cmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), usageFmt, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStderr(), nfs, 0)
		return nil
	})

	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStdout(), nfs, 0)
	})

	fs := cmd.Flags()
	for _, f := range nfs.FlagSets {
		fs.AddFlagSet(f)
	}
}

func (o *Options) addAppFlags(fs *pflag.FlagSet) {
	fs.StringVarP(&o.logLevel,
		"log-level", "v", "1",
		"Log level (1-5).")

	fs.IntVar(&o.ReadyzPort,
		"readiness-probe-port", 6060,
		"Port to expose the readiness probe on 0.0.0.0 on path `/readyz`.")

	fs.IntVar(&o.MetricsPort,
		"metrics-port", 9402,
		"Port to expose Prometheus metrics on 0.0.0.0 on path '/metrics'.")

	fs.StringVar(&o.DaprNamespace,
		"dapr-namespace", "dapr-system",
		"Namespace where Dapr is installed.")

	fs.StringVar(&o.TrustBundleCertificateName,
		"trust-bundle-certificate-name", "dapr-trust-bundle",
		"Name of the cert-manager Certificate which signs and manages the dapr trust bundle. Certificate must be in the same namespace as to where dapr is installed.")

	fs.StringVar(&o.TrustAnchorFilePath,
		"trust-anchor-file-path", "",
		"Optional name of the file which contains the trust anchor. If empty, the trust anchor will be sourced from the cert-manager Certificate.")
}
