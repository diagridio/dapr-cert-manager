package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	cmutil "github.com/cert-manager/cert-manager/pkg/api/util"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/diagridio/dapr-cert-manager-helper/pkg/trustanchor"
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

// Options configure the dapr-cert-manager-helper controllers.
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

// trustbundle is the dapr-trust-bundle Secret reconciler.
type trustbundle struct {
	log         logr.Logger
	lister      client.Reader
	client      client.Client
	trustAnchor x509bundle.Source
	certNN      types.NamespacedName
}

// Reconcile will ensure that the dapr trust-bundle Secret is updated with the
// latest issuer certificate. Will not delete the existing bundle if the
// cert-manager Secret has no data, and will only append to the trust anchor.
func (t *trustbundle) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// We should only ever be reconciling either the dapr trust-bundle Secret, or
	// the cert-manager Certificate Secret.
	// In either case, we consider the cert-manager Certificate Secret to be the
	// source of truth, and will update the dapr trust-bundle Secret with the
	// latest data.

	log := t.log.WithValues("reconciled_secret", req.NamespacedName)
	dbg := log.V(3)

	dbg.Info("reconciling")

	var cert cmapi.Certificate
	err := t.lister.Get(ctx, t.certNN, &cert)
	if apierrors.IsNotFound(err) {
		// The cert-manager Certificate resource does not exist, so we can't
		// do anything.
		dbg.Info("cert-manager Certificate resource does not exist")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	dbg.Info("found cert-manager Certificate resource", "cert", cert.Name)

	var cmSecret corev1.Secret
	err = t.lister.Get(ctx, types.NamespacedName{
		Namespace: cert.Namespace,
		Name:      cert.Spec.SecretName,
	}, &cmSecret)
	if apierrors.IsNotFound(err) {
		dbg.Info("cert-manager Secret does not exist", "secret", cert.Spec.SecretName)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	dbg.Info("found cert-manager Secret", "secret", cert.Spec.SecretName)

	var daprSecret corev1.Secret
	err = t.lister.Get(ctx, types.NamespacedName{
		Namespace: t.certNN.Namespace,
		Name:      "dapr-trust-bundle",
	}, &daprSecret)
	if apierrors.IsNotFound(err) {
		log.Error(err, "dapr trust-bundle Secret does not exist")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	dbg.Info("found dapr trust-bundle Secret")

	ta, shouldReconcile, err := t.shouldReconcileSecret(log, dbg, daprSecret, cmSecret)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !shouldReconcile {
		log.Info("dapr trust-bundle Secret is up to date")
		return ctrl.Result{}, nil
	}

	log.Info("updating dapr trust-bundle Secret")

	taPEM, err := ta.Marshal()
	if err != nil {
		// This error should never really happen since we just parsed the certs.
		// We are extra noisy here so its easier to pick up by the user and report
		// the bug.
		log.Error(err, "failed to marshal trust anchor, this error is a bug, please report the issue to Diagrid")
		return ctrl.Result{}, fmt.Errorf("this error is a bug, please report this issue to Diagrid: %w", err)
	}

	// Preserve existing keys in the dapr trust-bundle Secret since it might be
	// the case that the same Secret is used for the cert-manager Certificate for
	// example.
	if daprSecret.Data == nil {
		daprSecret.Data = make(map[string][]byte)
	}
	daprSecret.Data["issuer.crt"] = cmSecret.Data[corev1.TLSCertKey]
	daprSecret.Data["issuer.key"] = cmSecret.Data[corev1.TLSPrivateKeyKey]
	daprSecret.Data[cmmeta.TLSCAKey] = taPEM

	return ctrl.Result{}, t.client.Update(ctx, &daprSecret)
}

// shouldReconcileSecret returns true if the Secret should be reconciled.
// Also returns the trust anchors for which to update the dapr trust-bundle
// Secret with.
func (t *trustbundle) shouldReconcileSecret(log, dbg logr.Logger, daprSecret, cmSecret corev1.Secret) (*x509bundle.Bundle, bool, error) {
	var shouldReconcile bool

	// If the cert-manager Secret has no data, we can't do anything.
	if len(cmSecret.Data) == 0 ||
		len(cmSecret.Data[corev1.TLSCertKey]) == 0 ||
		len(cmSecret.Data[corev1.TLSPrivateKeyKey]) == 0 {
		dbg.Info("cert-manager Secret has no data")
		return nil, false, nil
	}

	if daprSecret.Data == nil ||
		!bytes.Equal(daprSecret.Data["issuer.crt"], cmSecret.Data[corev1.TLSCertKey]) ||
		!bytes.Equal(daprSecret.Data["issuer.key"], cmSecret.Data[corev1.TLSPrivateKeyKey]) {
		dbg.Info("data in dapr trust-bundle Secret does not match cert-manager Secret")
		shouldReconcile = true
	}

	// Ensure the dapr trust-bundle Secret has the trust anchor of the helper.
	var cmTA *x509bundle.Bundle
	if t.trustAnchor != nil {
		var err error
		cmTA, err = t.trustAnchor.GetX509BundleForTrustDomain(spiffeid.TrustDomain{})
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

	var daprTA *x509bundle.Bundle
	if len(daprSecret.Data["ca.crt"]) > 0 {
		var err error
		daprTA, err = x509bundle.Parse(spiffeid.TrustDomain{}, daprSecret.Data["ca.crt"])
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse trust anchor from dapr trust-bundle Secret: %w", err)
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
func AddTrustBundle(ctx context.Context, mgr ctrl.Manager, opts Options) error {
	log := opts.Log.WithName("controller").WithName("trust-bundle")
	lister := mgr.GetCache()

	tb := &trustbundle{
		log:         log,
		lister:      lister,
		client:      mgr.GetClient(),
		trustAnchor: opts.TrustAnchor,
		certNN: types.NamespacedName{
			Namespace: opts.DaprNamespace,
			Name:      opts.TrustBundleCertificateName,
		},
	}

	// TODO: @joshvanl add custom source to re-reconcile when the trust anchor
	// changes on file.

	controller := ctrl.NewControllerManagedBy(mgr).
		// Watch the target trust-bundle Secret.
		For(new(corev1.Secret), builder.OnlyMetadata, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetName() == "dapr-trust-bundle" && obj.GetNamespace() == opts.DaprNamespace
		}))).

		// Watch the trust-bundle Certificate resource. Reconcile the Secret on
		// update.
		Watches(&source.Kind{Type: new(cmapi.Certificate)}, handler.EnqueueRequestsFromMapFunc(
			func(obj client.Object) []ctrl.Request {
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
			// Only reconcile the cert-manager Certificate for the trust-bundle.
			return obj.GetName() == opts.TrustBundleCertificateName && obj.GetNamespace() == opts.DaprNamespace
		})))

	if opts.TrustAnchor != nil {
		controller.Watches(&source.Channel{Source: opts.TrustAnchor.EventChannel()}, handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
			return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: opts.DaprNamespace, Name: "dapr-trust-bundle"}}}
		}))
	}

	return controller.Complete(tb)
}
