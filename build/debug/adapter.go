package debug

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"

	"github.com/docker/buildx/build"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
)

var ErrRestart = stderrors.New("debug: restart")

type Adapter struct {
	threads      map[int]*thread
	threadsMu    sync.RWMutex
	nextThreadID int

	ctx    context.Context
	cancel func(err error)

	wg sync.WaitGroup
}

func NewAdapter() *Adapter {
	d := &Adapter{
		threads:      make(map[int]*thread),
		nextThreadID: 1,
	}
	d.ctx, d.cancel = context.WithCancelCause(context.Background())
	return d
}

func (d *Adapter) Stop() error {
	d.cancel(context.Canceled)
	return nil
}

func (d *Adapter) Evaluate(ctx context.Context, c gateway.Client, res *gateway.Result) error {
	errCh := make(chan error, 1)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()

		t := d.newThread()
		defer d.deleteThread(t)

		errCh <- t.Evaluate(ctx, c, res)
	}()

	select {
	case err := <-errCh:
		return err
	case <-d.ctx.Done():
		return d.ctx.Err()
	}
}

func (d *Adapter) newThread() (t *thread) {
	d.threadsMu.Lock()
	id := d.nextThreadID
	t = &thread{
		d:    d,
		id:   id,
		name: fmt.Sprintf("thread%d", id),
	}
	d.threads[t.id] = t
	d.nextThreadID++
	d.threadsMu.Unlock()
	return t
}

func (d *Adapter) deleteThread(t *thread) {
	d.threadsMu.Lock()
	delete(d.threads, t.id)
	d.threadsMu.Unlock()
}

func (d *Adapter) Handler() build.Handler {
	return build.Handler{
		Evaluate: d.Evaluate,
	}
}

type thread struct {
	d    *Adapter
	id   int
	name string

	paused chan struct{}
	mu     sync.Mutex
}

func (t *thread) Evaluate(ctx context.Context, c gateway.Client, res *gateway.Result) error {
	return res.EachRef(func(ref gateway.Reference) error {
		return ref.Evaluate(ctx)
	})
}
