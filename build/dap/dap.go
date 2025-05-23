package dap

import (
	"context"
	"io"
	"sync"

	"github.com/docker/buildx/build"
	"github.com/google/go-dap"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
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
		Solve: func(ctx context.Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
			resCh := make(chan *gateway.Result, 1)
			errCh := make(chan error, 1)

			srv.Go(func(ctx Context) {
				res, err := d.Solve(ctx, c, req)
				if err != nil {
					errCh <- err
				} else {
					resCh <- res
				}
			})

			select {
			case res := <-resCh:
				return res, nil
			case err := <-errCh:
				return nil, err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
}

type debugAdapter struct {
	initialized chan struct{}
	started     chan struct{}

	paused   chan struct{}
	pausedMu sync.Mutex
}

func newDebugAdapter() *debugAdapter {
	return &debugAdapter{
		initialized: make(chan struct{}),
		started:     make(chan struct{}),
	}
}

func (d *debugAdapter) Initialize(c Context, req *dap.InitializeRequest, resp *dap.InitializeResponse) error {
	close(d.initialized)

	c.Go(func(c2 Context) {
		// Wait for the initialize request to return and then send initialized
		// event immediately.
		<-c.Done()

		// Send initialized event.
		c2.C() <- &dap.InitializedEvent{
			Event: dap.Event{
				Event: "initialized",
			},
		}
	})
	return nil
}

func (d *debugAdapter) Launch(c Context, req *dap.LaunchRequest, resp *dap.LaunchResponse) error {
	close(d.started)
	return nil
}

func (d *debugAdapter) Attach(c Context, req *dap.AttachRequest, resp *dap.AttachResponse) error {
	close(d.started)
	return nil
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

func (d *debugAdapter) Solve(ctx Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
	res, err := build.Solve(ctx, c, req)

	if d.stopOnResult {
		paused := d.Pause(ctx)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-paused:
		}
	}

	return res, err
}

func (d *debugAdapter) Threads(c Context, req *dap.ThreadsRequest, resp *dap.ThreadsResponse) error {
	resp.Body.Threads = []dap.Thread{
		{
			Id:   1,
			Name: "main",
		},
	}
	return nil
}

func (d *debugAdapter) Pause(c Context) <-chan struct{} {
	d.pausedMu.Lock()
	defer d.pausedMu.Unlock()

	if d.paused == nil {
		d.paused = make(chan struct{})
	}

	c.C() <- &dap.StoppedEvent{
		Event: dap.Event{
			Event: "stopped",
		},
		Body: dap.StoppedEventBody{
			Reason:      "pause",
			Description: "Build completed",
			ThreadId:    1,
		},
	}
	return d.paused
}

func (d *debugAdapter) Handler() Handler {
	return Handler{
		Initialize: d.Initialize,
		Launch:     d.Launch,
		Attach:     d.Attach,
		Continue:   d.Continue,
		Threads:    d.Threads,
	}
}
