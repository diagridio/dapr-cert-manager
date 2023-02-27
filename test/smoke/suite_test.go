package smoke

import (
	"bytes"
	"context"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Smoke", func() {
	It("the helper should update the trust-bundle Secret with a new issuer key and cert, but preserve the old root CA", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).NotTo(HaveOccurred())
		Expect(cmapi.AddToScheme(scheme)).NotTo(HaveOccurred())

		cl, err := client.New(cnf.RestConfig, client.Options{
			Scheme: scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		var (
			cert       cmapi.Certificate
			cmSecret   corev1.Secret
			daprSecret corev1.Secret
		)

		// dapr-trust-bundle
		Eventually(func() bool {
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: cnf.CertificateNameTrustBundle}, &cert)).NotTo(HaveOccurred())

			err := cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: cert.Spec.SecretName}, &cmSecret)
			if err != nil {
				return false
			}

			Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: "dapr-trust-bundle"}, &daprSecret)).NotTo(HaveOccurred())

			return bytes.Equal(cmSecret.Data["tls.crt"], daprSecret.Data["issuer.crt"]) && bytes.Equal(cmSecret.Data["tls.key"], daprSecret.Data["issuer.key"])
		}, "10s", "100ms").Should(BeTrue(), "the trust-bundle secret should have been updated with the cert-manager issuer key and cert")

		cmCA, err := x509bundle.Parse(spiffeid.TrustDomain{}, cmSecret.Data["ca.crt"])
		Expect(err).NotTo(HaveOccurred())
		Expect(cmCA.X509Authorities()).To(HaveLen(1))

		daprCA, err := x509bundle.Parse(spiffeid.TrustDomain{}, daprSecret.Data["ca.crt"])
		Expect(err).NotTo(HaveOccurred())
		Expect(daprCA.X509Authorities()).To(HaveLen(2))
		Expect(daprCA.HasX509Authority(cmCA.X509Authorities()[0])).To(BeTrue(), "the trust-bundle secret should have the same root CA as the cert-manager issuer")

		// dapr-webhook-cert
		Eventually(func() bool {
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: cnf.CertificateNameWebhook}, &cert)).NotTo(HaveOccurred())

			err := cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: cert.Spec.SecretName}, &cmSecret)
			if err != nil {
				return false
			}

			Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: "dapr-webhook-cert"}, &daprSecret)).NotTo(HaveOccurred())

			return bytes.Equal(cmSecret.Data["tls.crt"], daprSecret.Data["tls.crt"]) && bytes.Equal(cmSecret.Data["tls.key"], daprSecret.Data["tls.key"])
		}, "10s", "100ms").Should(BeTrue(), "the webook secret should have been updated with the cert-manager issuer key and cert")

		Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: "dapr-webhook-ca"}, &daprSecret)).NotTo(HaveOccurred())

		cmCA, err = x509bundle.Parse(spiffeid.TrustDomain{}, cmSecret.Data["ca.crt"])
		Expect(err).NotTo(HaveOccurred())
		Expect(cmCA.X509Authorities()).To(HaveLen(1))

		daprCA, err = x509bundle.Parse(spiffeid.TrustDomain{}, daprSecret.Data["caBundle"])
		Expect(err).NotTo(HaveOccurred())
		Expect(daprCA.X509Authorities()).To(HaveLen(2))
		Expect(daprCA.HasX509Authority(cmCA.X509Authorities()[0])).To(BeTrue(), "the webhook secret should have the same root CA as the cert-manager issuer")

		// dapr-sidecar-injector-cert
		Eventually(func() bool {
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: cnf.CertificateNameSidecarInjector}, &cert)).NotTo(HaveOccurred())

			err := cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: cert.Spec.SecretName}, &cmSecret)
			if err != nil {
				return false
			}

			Expect(cl.Get(ctx, client.ObjectKey{Namespace: cnf.DaprNamespace, Name: "dapr-sidecar-injector-cert"}, &daprSecret)).NotTo(HaveOccurred())

			return bytes.Equal(cmSecret.Data["tls.crt"], daprSecret.Data["tls.crt"]) && bytes.Equal(cmSecret.Data["tls.key"], daprSecret.Data["tls.key"])
		}, "10s", "100ms").Should(BeTrue(), "the sidecar-injector secret should have been updated with the cert-manager issuer key and cert")
	})
})
