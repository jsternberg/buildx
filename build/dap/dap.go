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
	initialized   chan struct{}
	started       chan struct{}
	configuration chan struct{}

	paused   chan struct{}
	pausedMu sync.Mutex

	solveReqCh chan *solveRequest
}

func newDebugAdapter() *debugAdapter {
	return &debugAdapter{
		initialized:   make(chan struct{}),
		started:       make(chan struct{}),
		configuration: make(chan struct{}),
		solveReqCh:    make(chan *solveRequest),
	}
}

func (d *debugAdapter) Initialize(c Context, req *dap.InitializeRequest, resp *dap.InitializeResponse) error {
	close(d.initialized)

	// Set capabilities.
	resp.Body.SupportsConfigurationDoneRequest = true
	return nil
}

func (d *debugAdapter) Launch(c Context, req *dap.LaunchRequest, resp *dap.LaunchResponse) error {
	close(d.started)

	// Send initialized event to tell the debug adapter
	// to send configuration.
	c.C() <- &dap.InitializedEvent{
		Event: dap.Event{
			Event: "initialized",
		},
	}

	c.Go(d.launch)
	return nil
}

func (d *debugAdapter) Attach(c Context, req *dap.AttachRequest, resp *dap.AttachResponse) error {
	close(d.started)

	c.Go(d.launch)
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
		case req := <-d.solveReqCh:
			if req == nil {
				return
			}

			c.Go(func(c Context) {
				defer close(req.resCh)
				defer close(req.errCh)

				res, err := d.solve(c, req.c, req.req)
				if err != nil {
					req.errCh <- err
					return
				}
				req.resCh <- res
			})
		}
	}
}

type solveRequest struct {
	c     gateway.Client
	req   gateway.SolveRequest
	resCh chan<- *gateway.Result
	errCh chan<- error
}

func (d *debugAdapter) solve(ctx Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
	// TODO: do debug things here such as catching errors.
	return build.Solve(ctx, c, req)
}

func (d *debugAdapter) Solve(ctx context.Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error) {
	resCh := make(chan *gateway.Result)
	errCh := make(chan error)

	// Send a solve request to the launch routine
	// which will perform the solve in the context of the server.
	sreq := &solveRequest{
		c:     c,
		req:   req,
		resCh: resCh,
		errCh: errCh,
	}
	select {
	case d.solveReqCh <- sreq:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Wait for the response.
	select {
	case res := <-resCh:
		return res, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
		Initialize:        d.Initialize,
		Launch:            d.Launch,
		Attach:            d.Attach,
		Continue:          d.Continue,
		SetBreakpoints:    d.SetBreakpoints,
		ConfigurationDone: d.ConfigurationDone,
		Threads:           d.Threads,
	}
}
