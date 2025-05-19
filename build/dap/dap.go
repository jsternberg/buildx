package dap

import (
	"context"
	"io"

	"github.com/docker/buildx/build"
	"github.com/google/go-dap"
	"golang.org/x/sync/errgroup"
)

func New(rdwr io.ReadWriter) build.Handler {
	initialized := make(chan struct{})
	started := make(chan struct{})
	srv := NewServer(Handler{
		Initialize: func(c Context, req *dap.InitializeRequest, resp *dap.InitializeResponse) error {
			close(initialized)

			c.Go(func(c2 Context) {
				// Wait for the initialize request to return and then send initialized
				// event immediately.
				<-c.Done()

				// Send initialized event.
				c2.C() <- &dap.InitializedEvent{}
			})
			return nil
		},
		Launch: func(c Context, req *dap.LaunchRequest, resp *dap.LaunchResponse) error {
			close(started)
			return nil
		},
		Attach: func(c Context, req *dap.AttachRequest, resp *dap.AttachResponse) error {
			close(started)
			return nil
		},
	})

	eg, ctx := errgroup.WithContext(context.Background())
	return build.Handler{
		OnStart: func() {
			eg.Go(func() error {
				return srv.Serve(ctx, rdwr)
			})

			<-initialized
			<-started
		},
		OnExit: func(exitCode int) {
			srv.Go(func(c Context) {
				c.C() <- &dap.TerminatedEvent{}
				c.C() <- &dap.ExitedEvent{
					Body: dap.ExitedEventBody{
						ExitCode: exitCode,
					},
				}
			})
			srv.Stop()

			eg.Wait()
		},
	}
}
