# dapr-cert-manager

dapr-cert-manager is a simple controller to allow [dapr](https://dapr.io)
installations to use Certificates originating from
[cert-manager](https://cert-manager.io). This controller watches a
cert-manager Certificate object for the root `dapr-trust-bundle`.

As and when the corresponding cert-manager Certificate object becomes ready or
renews, dapr-cert-manager will update the respective Secret object with the
latest certificate and key.

Root CA certificates are always appended to, and never replaced.

dapr-cert-manager can also optionally replace the root CA certificates in the
target Secret with a custom CA certificate from file.

---

## Installation

Ensure cert-manager is [installed](https://cert-manager.io/docs/installation/),
and the corresponding Certificates have been created.

Please see [the example manifest](./test/smoke/cert-manager-certs.yaml) for an
example of how your cert-manager Certificates could be arranged.

The [helm values file](./deploy/charts/dapr-cert-manager/values.yaml) shows all
available configuration options.

```bash
  helm upgrade -i dapr-cert-manager ./deploy/charts/dapr-cert-manager \
    --namespace dapr-cert-manager \
    --create-namespace \
    --set app.trustBundleCertificateName=dapr-trust-bundle \
    --wait
```
