package dap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/docker/buildx/build"
	"github.com/google/go-dap"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/errdefs"
	"golang.org/x/sync/errgroup"
)

func New(rdwr io.ReadWriter) build.Handler {
	d := newDebugAdapter()
	srv := NewServer(d.Handler())

	eg, ctx := errgroup.WithContext(context.Background())
	return build.Handler{
		OnStart: func() {
			eg.Go(func() error {
				return srv.Serve(ctx, rdwr)
			})

			<-d.initialized
			<-d.started
		},
		OnExit: func(exitCode int) {
			srv.Go(func(c Context) {
				c.C() <- &dap.TerminatedEvent{
					Event: dap.Event{
						Event: "terminated",
					},
				}
				c.C() <- &dap.ExitedEvent{
					Event: dap.Event{
						Event: "exited",
					},
					Body: dap.ExitedEventBody{
						ExitCode: exitCode,
					},
				}
			})
			srv.Stop()

			eg.Wait()
		},
		Evaluate: func(ctx context.Context, c gateway.Client, res *gateway.Result) error {
			errCh := make(chan error, 1)

			srv.Go(func(ctx Context) {
				errCh <- d.Evaluate(ctx, c, res)
			})

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

type debugAdapter struct {
	initialized   chan struct{}
	started       chan struct{}
	configuration chan struct{}

	evaluateReqCh chan *evaluateRequest

	threads      map[int]*thread
	threadsMu    sync.RWMutex
	nextThreadID int
}

type thread struct {
	id   int
	name string

	paused chan struct{}
	ref    gateway.Reference
	mu     sync.Mutex
}

func (t *thread) Pause(c Context, reason, desc string) <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.paused == nil {
		t.paused = make(chan struct{})
	}

	c.C() <- &dap.StoppedEvent{
		Event: dap.Event{Event: "stopped"},
		Body: dap.StoppedEventBody{
			Reason:      reason,
			Description: desc,
			ThreadId:    t.id,
		},
	}
	return t.paused
}

func newDebugAdapter() *debugAdapter {
	return &debugAdapter{
		initialized:   make(chan struct{}),
		started:       make(chan struct{}),
		configuration: make(chan struct{}),
		evaluateReqCh: make(chan *evaluateRequest),
		threads:       make(map[int]*thread),
		nextThreadID:  1,
	}
}

func (d *debugAdapter) Initialize(c Context, req *dap.InitializeRequest, resp *dap.InitializeResponse) error {
	close(d.initialized)

	// Set capabilities.
	resp.Body.SupportsConfigurationDoneRequest = true
	return nil
}

func (d *debugAdapter) Launch(c Context, req *dap.LaunchRequest, resp *dap.LaunchResponse) error {
	d.start(c)
	return nil
}

func (d *debugAdapter) Attach(c Context, req *dap.AttachRequest, resp *dap.AttachResponse) error {
	d.start(c)
	return nil
}

func (d *debugAdapter) Disconnect(c Context, req *dap.DisconnectRequest, resp *dap.DisconnectResponse) error {
	close(d.evaluateReqCh)
	return nil
}

func (d *debugAdapter) start(c Context) {
	close(d.started)

	// Send initialized event to tell the debug adapter
	// to send configuration.
	c.C() <- &dap.InitializedEvent{
		Event: dap.Event{
			Event: "initialized",
		},
	}

	c.Go(d.launch)
}

func (d *debugAdapter) Continue(c Context, req *dap.ContinueRequest, resp *dap.ContinueResponse) error {
	d.pausedMu.Lock()
	defer d.pausedMu.Unlock()

	if d.paused != nil {
		close(d.paused)
		d.paused = nil
	}
	return nil
}

func (d *debugAdapter) SetBreakpoints(c Context, req *dap.SetBreakpointsRequest, resp *dap.SetBreakpointsResponse) error {
	return nil
}

func (d *debugAdapter) ConfigurationDone(c Context, req *dap.ConfigurationDoneRequest, resp *dap.ConfigurationDoneResponse) error {
	d.configuration <- struct{}{}
	close(d.configuration)
	return nil
}

func (d *debugAdapter) launch(c Context) {
	// Send initialized event.
	c.C() <- &dap.InitializedEvent{
		Event: dap.Event{
			Event: "initialized",
		},
	}

	// Wait for configuration.
	select {
	case <-c.Done():
		return
	case <-d.configuration:
		// TODO: actual configuration
	}

	for {
		select {
		case <-c.Done():
			return
		case req := <-d.evaluateReqCh:
			if req == nil {
				return
			}

			t := d.newThread(c)
			c.Go(func(c Context) {
				defer d.deleteThread(c, t)
				defer close(req.errCh)
				req.errCh <- d.evaluate(c, t, req.c, req.res)
			})
		}
	}
}

func (d *debugAdapter) newThread(ctx Context) (t *thread) {
	d.threadsMu.Lock()
	id := d.nextThreadID
	t = &thread{
		id:   id,
		name: fmt.Sprintf("thread%d", id),
	}
	d.threads[t.id] = t
	d.nextThreadID++
	d.threadsMu.Unlock()

	ctx.C() <- &dap.ThreadEvent{
		Event: dap.Event{Event: "thread"},
		Body: dap.ThreadEventBody{
			Reason:   "started",
			ThreadId: t.id,
		},
	}
	return t
}

func (d *debugAdapter) deleteThread(ctx Context, t *thread) {
	d.threadsMu.Lock()
	delete(d.threads, t.id)
	d.threadsMu.Unlock()

	ctx.C() <- &dap.ThreadEvent{
		Event: dap.Event{Event: "thread"},
		Body: dap.ThreadEventBody{
			Reason:   "exited",
			ThreadId: t.id,
		},
	}
}

type evaluateRequest struct {
	c     gateway.Client
	res   *gateway.Result
	errCh chan<- error
}

func (d *debugAdapter) evaluate(ctx Context, t *thread, c gateway.Client, res *gateway.Result) error {
	return res.EachRef(func(ref gateway.Reference) error {
		var paused <-chan struct{}
		err := ref.Evaluate(ctx)
		if err != nil {
			var solveErr errdefs.SolveError
			if errors.As(err, &solveErr) {
				paused = t.Pause(ctx, "exception", "Encountered an error during build")
			}
		} else {
			paused = t.Pause(ctx, "pause", "Build completed")
		}

		select {
		case <-paused:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

func (d *debugAdapter) Evaluate(ctx context.Context, c gateway.Client, res *gateway.Result) error {
	errCh := make(chan error)

	// Send a solve request to the launch routine
	// which will perform the solve in the context of the server.
	ereq := &evaluateRequest{
		c:     c,
		res:   res,
		errCh: errCh,
	}
	select {
	case d.evaluateReqCh <- ereq:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Wait for the response.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *debugAdapter) Threads(c Context, req *dap.ThreadsRequest, resp *dap.ThreadsResponse) error {
	d.threadsMu.RLock()
	defer d.threadsMu.RUnlock()

	for _, t := range d.threads {
		resp.Body.Threads = append(resp.Body.Threads, dap.Thread{
			Id:   t.id,
			Name: t.name,
		})
	}
	return nil
}

func (d *debugAdapter) Handler() Handler {
	return Handler{
		Initialize:        d.Initialize,
		Launch:            d.Launch,
		Attach:            d.Attach,
		Continue:          d.Continue,
		SetBreakpoints:    d.SetBreakpoints,
		ConfigurationDone: d.ConfigurationDone,
		Threads:           d.Threads,
	}
}
