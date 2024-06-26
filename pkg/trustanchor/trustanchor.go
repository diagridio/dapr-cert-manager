package trustanchor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/dapr/kit/fswatcher"
	"github.com/go-logr/logr"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Options struct {
	Log logr.Logger

	// TrustBundlePath is the path to the trust bundle file.
	TrustBundlePath string
}

type Interface interface {
	x509bundle.Source
	manager.LeaderElectionRunnable
	manager.Runnable
	EventChannel() <-chan event.GenericEvent
}

type internal struct {
	log    logr.Logger
	path   string
	bundle *x509bundle.Bundle
	lock   sync.RWMutex
	env    []chan<- event.GenericEvent
	wg     sync.WaitGroup
}

func New(ops Options) Interface {
	return &internal{
		log:  ops.Log.WithName("trustanchor"),
		path: ops.TrustBundlePath,
	}
}

func (i *internal) Start(ctx context.Context) error {
	defer i.wg.Done()
	i.log.Info("starting trust anchor manager")

	// Load the trust bundle from the file.
	bundle, err := x509bundle.Load(spiffeid.TrustDomain{}, i.path)
	if err != nil {
		return fmt.Errorf("failed to load trust bundle from file %q: %w", i.path, err)
	}

	fs, err := fswatcher.New(fswatcher.Options{
		Targets: []string{filepath.Dir(i.path)},
	})
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	i.updateBundle(ctx, bundle)

	errCh := make(chan error)
	eventCh := make(chan struct{})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer cancel()
		err := fs.Run(ctx, eventCh)
		// Ignore context canceled errors.
		if errors.Is(err, context.Canceled) {
			err = nil
		}
		errCh <- err
	}()

	for {
		select {
		case <-ctx.Done():
			i.log.Info("stopping trust anchor manager")
			return <-errCh
		case <-eventCh:
			bundle, err := x509bundle.Load(spiffeid.TrustDomain{}, i.path)
			if err != nil {
				cancel()
				return errors.Join(err, <-errCh)
			}
			i.updateBundle(ctx, bundle)
		}
	}
}

// We want to load the trust anchors, even if we are not the leader.
func (i *internal) NeedLeaderElection() bool {
	return false
}

func (i *internal) updateBundle(ctx context.Context, bundle *x509bundle.Bundle) {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.bundle = bundle

	i.wg.Add(len(i.env))
	for _, env := range i.env {
		go func(env chan<- event.GenericEvent) {
			defer i.wg.Done()
			select {
			case env <- event.GenericEvent{}:
			case <-ctx.Done():
			}
		}(env)
	}
}

func (i *internal) EventChannel() <-chan event.GenericEvent {
	i.lock.Lock()
	defer i.lock.Unlock()
	env := make(chan event.GenericEvent)
	i.env = append(i.env, env)
	return env
}

func (i *internal) GetX509BundleForTrustDomain(_ spiffeid.TrustDomain) (*x509bundle.Bundle, error) {
	i.lock.RLock()
	defer i.lock.RUnlock()

	if i.bundle == nil {
		return nil, fmt.Errorf("trust bundle is not yet loaded from file %q", i.path)
	}

	return i.bundle, nil
}
