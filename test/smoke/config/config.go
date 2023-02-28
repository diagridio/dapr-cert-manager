package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Config struct {
	kubeConfig string

	DaprNamespace                  string
	CertificateNameTrustBundle     string
	CertificateNameWebhook         string
	CertificateNameSidecarInjector string
	RestConfig                     *rest.Config
}

func New(fs *flag.FlagSet) *Config {
	return new(Config).addFlags(fs)
}

func (c *Config) Complete() error {
	if c.kubeConfig == "" {
		return fmt.Errorf("--kubeconfig-path must not be empty")
	}

	var err error
	c.RestConfig, err = clientcmd.BuildConfigFromFlags("", c.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to build kubernetes rest config from %q: %s", c.kubeConfig, err)
	}

	return nil
}

func (c *Config) addFlags(fs *flag.FlagSet) *Config {
	kubeConfigFile := os.Getenv(clientcmd.RecommendedConfigPathEnvVar)
	if kubeConfigFile == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			panic("Failed to get user home directory: " + err.Error())
		}
		kubeConfigFile = filepath.Join(homeDir, clientcmd.RecommendedHomeDir, clientcmd.RecommendedFileName)
	}

	fs.StringVar(&c.kubeConfig, "kubeconfig-path", kubeConfigFile, "Path to config containing embedded authinfo for kubernetes. Default value is from environment variable "+clientcmd.RecommendedConfigPathEnvVar)
	fs.StringVar(&c.DaprNamespace, "dapr-namespace", "dapr-system", "The namespace where dapr is installed")
	fs.StringVar(&c.CertificateNameTrustBundle, "certificate-name-trust-bundle", "dapr-trust-bundle", "The name of the trust-bundle cert-manager Certificate object that is managing the issuer key")
	fs.StringVar(&c.CertificateNameWebhook, "certificate-name-webhook", "dapr-webhook", "The name of the cert-manager webhook Certificate object that is managing the issuer key")
	fs.StringVar(&c.CertificateNameSidecarInjector, "certificate-name-sidecar-injector", "dapr-sidecar-injector", "The name of the cert-manager sidecar-injector Certificate object that is managing the issuer key")
	return c
}
