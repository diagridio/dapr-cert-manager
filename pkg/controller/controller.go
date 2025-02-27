package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	cmutil "github.com/cert-manager/cert-manager/pkg/api/util"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/diagridio/dapr-cert-manager/pkg/trustanchor"
	"github.com/go-logr/logr"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Options configure the dapr-cert-manager controllers.
type Options struct {
	// Log is the logger used by the controllers.
	Log logr.Logger

	// DaprNamespace is the namespace that dapr is installed in.
	// Required.
	DaprNamespace string

	// TrustBundleCertificateName is the name of the cert-manager Certificate
	// resource that is used to generate the trust-bundle. Must be in the same
	// namespace as the dapr installation.
	// Required.
	TrustBundleCertificateName string

	// TrustAnchor is used for the trust-bundle trust anchors. If empty the nil,
	// the `ca.crt` created by cert-manager will be used.
	TrustAnchor trustanchor.Interface
}

// secretCtrl is the controller that manages dapr certificate secrets.
type secretCtrl struct {
	log           logr.Logger
	lister        client.Reader
	client        client.Client
	trustAnchor   x509bundle.Source
	daprNamespace string

	confs []secretConf
}

type secretConf struct {
	certName        string
	certSecretName  string
	caSecretName    string
	certSectretKey  string
	certSecretPKKey string
	certSecretCAKey string
}

// Reconcile will ensure that the dapr trust-bundle Secret is updated with the
// latest issuer certificate. Will not delete the existing bundle if the
// cert-manager Secret has no data, and will only append to the trust anchor.
func (s *secretCtrl) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// We should only ever be reconciling either the dapr trust-bundle Secret, or
	// the cert-manager Certificate Secret.
	// In either case, we consider the cert-manager Certificate Secret to be the
	// source of truth, and will update the dapr trust-bundle Secret with the
	// latest data.

	log := s.log.WithValues("reconciled_secret", req.NamespacedName)

	var (
		wg   sync.WaitGroup
		lock sync.Mutex
		errs []error
	)

	wg.Add(len(s.confs))
	for _, conf := range s.confs {
		go func(conf secretConf) {
			defer wg.Done()
			if err := s.reconcileBundle(ctx, log, conf); err != nil {
				lock.Lock()
				errs = append(errs, err)
				lock.Unlock()
			}
		}(conf)
	}

	wg.Wait()
	if len(errs) > 0 {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile: %v", errors.Join(errs...))
	}
	return ctrl.Result{}, nil
}

func (s *secretCtrl) reconcileBundle(ctx context.Context, log logr.Logger, conf secretConf) error {
	log = log.WithValues("cert_name", conf.certName)
	dbg := log.V(3)

	dbg.Info("reconciling")

	var cert cmapi.Certificate
	err := s.lister.Get(ctx, types.NamespacedName{Namespace: s.daprNamespace, Name: conf.certName}, &cert)
	if apierrors.IsNotFound(err) {
		// The cert-manager Certificate resource does not exist, so we can't
		// do anything.
		dbg.Info("cert-manager Certificate resource does not exist")
		return nil
	}
	if err != nil {
		return err
	}

	dbg.Info("found cert-manager Certificate resource")

	var cmSecret corev1.Secret
	err = s.lister.Get(ctx, types.NamespacedName{
		Namespace: cert.Namespace,
		Name:      cert.Spec.SecretName,
	}, &cmSecret)
	if apierrors.IsNotFound(err) {
		dbg.Info("cert-manager Secret does not exist", "secret", cert.Spec.SecretName)
		return nil
	}
	if err != nil {
		return err
	}

	dbg.Info("found cert-manager Secret", "secret", cert.Spec.SecretName)

	var daprCertSecret corev1.Secret
	err = s.lister.Get(ctx, types.NamespacedName{
		Namespace: s.daprNamespace,
		Name:      conf.certSecretName,
	}, &daprCertSecret)
	if apierrors.IsNotFound(err) {
		log.Error(err, "dapr certificate Secret does not exist")
		return nil
	}
	if err != nil {
		return err
	}

	var daprCASecret corev1.Secret
	if len(conf.caSecretName) > 0 {
		if conf.caSecretName == conf.certSecretName {
			daprCASecret = daprCertSecret
		} else {
			err = s.lister.Get(ctx, types.NamespacedName{
				Namespace: s.daprNamespace,
				Name:      conf.caSecretName,
			}, &daprCASecret)
			if apierrors.IsNotFound(err) {
				log.Error(err, "dapr CA certificate Secret does not exist")
				return nil
			}
			if err != nil {
				return err
			}
		}
	}

	dbg.Info("found dapr certificate Secret")

	ta, shouldReconcile, err := s.shouldReconcileSecret(log, dbg, conf, daprCertSecret, daprCASecret, cmSecret)
	if err != nil {
		return err
	}

	if !shouldReconcile {
		log.Info("dapr trust-bundle Secret is up to date")
		return nil
	}

	log.Info("updating dapr certificate Secret")

	// Preserve existing keys in the dapr certificate Secret since it might be
	// the case that the same Secret is used for the cert-manager Certificate for
	// example.
	if daprCertSecret.Data == nil {
		daprCertSecret.Data = make(map[string][]byte)
	}
	daprCertSecret.Data[conf.certSectretKey] = cmSecret.Data[corev1.TLSCertKey]
	daprCertSecret.Data[conf.certSecretPKKey] = cmSecret.Data[corev1.TLSPrivateKeyKey]

	if err := s.client.Update(ctx, &daprCertSecret); err != nil {
		return err
	}

	if len(conf.caSecretName) == 0 {
		return nil
	}

	taPEM, err := ta.Marshal()
	if err != nil {
		// This error should never really happen since we just parsed the certs.
		// We are extra noisy here so its easier to pick up by the user and report
		// the bug.
		log.Error(err, "failed to marshal trust anchor, this error is a bug, please report the issue to Diagrid")
		return fmt.Errorf("this error is a bug, please report this issue to Diagrid: %w", err)
	}

	if conf.caSecretName == conf.certSecretName {
		daprCASecret = daprCertSecret
	}

	if daprCASecret.Data == nil {
		daprCASecret.Data = make(map[string][]byte)
	}
	daprCASecret.Data[conf.certSecretCAKey] = taPEM

	return s.client.Update(ctx, &daprCASecret)
}

// shouldReconcileSecret returns true if the Secret should be reconciled.
// Also returns the trust anchors for which to update the dapr trust-bundle
// Secret with.
func (s *secretCtrl) shouldReconcileSecret(log, dbg logr.Logger,
	conf secretConf,
	daprCertSecret, daprCASecret, cmSecret corev1.Secret,
) (*x509bundle.Bundle, bool, error) {
	var shouldReconcile bool

	// If the cert-manager Secret has no data, we can't do anything.
	if len(cmSecret.Data) == 0 ||
		len(cmSecret.Data[corev1.TLSCertKey]) == 0 ||
		len(cmSecret.Data[corev1.TLSPrivateKeyKey]) == 0 {
		dbg.Info("cert-manager Secret has no data")
		return nil, false, nil
	}

	if daprCertSecret.Data == nil ||
		!bytes.Equal(daprCertSecret.Data[conf.certSectretKey], cmSecret.Data[corev1.TLSCertKey]) ||
		!bytes.Equal(daprCertSecret.Data[conf.certSecretPKKey], cmSecret.Data[corev1.TLSPrivateKeyKey]) {
		dbg.Info("data in dapr certificate Secret does not match cert-manager Secret")
		shouldReconcile = true
	}

	var daprTA *x509bundle.Bundle
	if len(conf.caSecretName) > 0 {
		// Ensure the dapr trust-bundle Secret has the trust anchor of the helper.
		var cmTA *x509bundle.Bundle
		if s.trustAnchor != nil {
			var err error
			cmTA, err = s.trustAnchor.GetX509BundleForTrustDomain(spiffeid.TrustDomain{})
			if err != nil {
				return nil, false, err
			}
		} else {
			if len(cmSecret.Data["ca.crt"]) > 0 {
				// If we don't have a static trust anchor, then use that from the
				// cert-manager Secret.
				var err error
				cmTA, err = x509bundle.Parse(spiffeid.TrustDomain{}, cmSecret.Data[cmmeta.TLSCAKey])
				if err != nil {
					return nil, false, fmt.Errorf("failed to parse trust anchor from cert-manager Secret: %w", err)
				}
			} else {
				log.Error(errors.New("no trust anchor found in cert-manager Certificate"), "the dapr root trust anchor may be empty!")
				cmTA = x509bundle.New(spiffeid.TrustDomain{})
			}
		}

		if len(daprCASecret.Data[conf.certSecretCAKey]) > 0 {
			var err error
			daprTA, err = x509bundle.Parse(spiffeid.TrustDomain{}, daprCASecret.Data[conf.certSecretCAKey])
			if err != nil {
				return nil, false, fmt.Errorf("failed to parse trust anchor from dapr certificate Secret: %w", err)
			}
		} else {
			daprTA = x509bundle.New(spiffeid.TrustDomain{})
		}

		for _, cert := range cmTA.X509Authorities() {
			if !daprTA.HasX509Authority(cert) {
				shouldReconcile = true
				daprTA.AddX509Authority(cert)
				dbg.Info("dapr trust-bundle Secret is missing trust anchor")
			}
		}
	}

	if shouldReconcile {
		return daprTA, true, nil
	}

	dbg.Info("dapr trust-bundle Secret has correct issuer and all required trust anchor")

	// TODO: @joshvanl do validation to ensure that the certificates are
	// appropriate.

	return nil, false, nil
}

// AddTrustBundle will register the trust-bundle controller with the
// controller-manager Manager.
// The trust-bundle controller will reconcile the target trust-bundle
// cert-manager Certificate resource, and ensure that the dapr trust-bundle is
// updated with the latest issuer certificate.
// Trust anchors are always appended to the trust-bundle, and never removed.
func AddTrustBundle(mgr ctrl.Manager, opts Options) error {
	log := opts.Log.WithName("controller").WithName("trust-bundle")
	lister := mgr.GetCache()

	secCtl := &secretCtrl{
		log:           log,
		lister:        lister,
		client:        mgr.GetClient(),
		trustAnchor:   opts.TrustAnchor,
		daprNamespace: opts.DaprNamespace,
	}
	if len(opts.TrustBundleCertificateName) > 0 {
		secCtl.confs = append(secCtl.confs, secretConf{
			certName:        opts.TrustBundleCertificateName,
			certSecretName:  "dapr-trust-bundle",
			caSecretName:    "dapr-trust-bundle",
			certSectretKey:  "issuer.crt",
			certSecretPKKey: "issuer.key",
			certSecretCAKey: "ca.crt",
		})
	}

	if len(secCtl.confs) == 0 {
		return errors.New("no certificate names provided")
	}

	// TODO: @joshvanl add custom source to re-reconcile when the trust anchor
	// changes on file.

	controller := ctrl.NewControllerManagedBy(mgr).
		// Watch the target trust-bundle Secret.
		For(new(corev1.Secret), builder.OnlyMetadata, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == opts.DaprNamespace && obj.GetName() == "dapr-trust-bundle"
		}))).

		// Watch the trust-bundle Certificate resource. Reconcile the Secret on
		// update.
		Watches(new(cmapi.Certificate), handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []ctrl.Request {
				var cert cmapi.Certificate
				err := lister.Get(ctx, client.ObjectKeyFromObject(obj), &cert)
				if apierrors.IsNotFound(err) {
					// Do nothing if the Certificate does not exist.
					return nil
				}

				if err != nil {
					// Log the error and do nothing.
					// This is likely a network error, so we will get another event when
					// the cache is updated.
					log.Error(err, "failed to get Certificate", "name", obj.GetName(), "namespace", obj.GetNamespace())
					return nil
				}

				if !cmutil.CertificateHasCondition(&cert, cmapi.CertificateCondition{
					Type:   cmapi.CertificateConditionReady,
					Status: cmmeta.ConditionTrue,
				}) {
					log.Info("Certificate is not ready yet", "name", obj.GetName(), "namespace", obj.GetNamespace())
					// Don't bother reconciling if the Certificate is not ready.
					return nil
				}

				// Reconcile the cert-manager Certificate Secret.
				return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: cert.Spec.SecretName, Namespace: obj.GetNamespace()}}}
			},
		), builder.OnlyMetadata, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			// Only reconcile the cert-manager Certificates we are watching.
			return obj.GetNamespace() == opts.DaprNamespace && obj.GetName() == opts.TrustBundleCertificateName
		})))

	if opts.TrustAnchor != nil {
		controller = controller.WatchesRawSource(source.Channel(
			opts.TrustAnchor.EventChannel(),
			handler.EnqueueRequestsFromMapFunc(
				func(_ context.Context, obj client.Object) []ctrl.Request {
					return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: opts.DaprNamespace, Name: "dapr-trust-bundle"}}}
				})))
	}

	return controller.Complete(secCtl)
}
