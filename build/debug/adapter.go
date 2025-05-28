package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/docker/buildx/build"
	"github.com/google/go-dap"
	"github.com/google/shlex"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type Adapter struct {
	srv *Server
	eg  *errgroup.Group
	cfg Config

	initialized   chan struct{}
	started       chan struct{}
	configuration chan struct{}

	evaluateReqCh chan *evaluateRequest

	threads      map[int]*thread
	threadsMu    sync.RWMutex
	nextThreadID int
}

func NewAdapter(cfg Config) *Adapter {
	d := &Adapter{
		cfg:           cfg,
		initialized:   make(chan struct{}),
		started:       make(chan struct{}),
		configuration: make(chan struct{}),
		evaluateReqCh: make(chan *evaluateRequest),
		threads:       make(map[int]*thread),
		nextThreadID:  1,
	}
	d.srv = NewServer(d.dapHandler())
	return d
}

func (d *Adapter) Start(ctx context.Context, conn Conn) {
	d.eg, _ = errgroup.WithContext(ctx)
	d.eg.Go(func() error {
		return d.srv.Serve(ctx, conn)
	})

	<-d.initialized
	<-d.started
}

func (d *Adapter) Stop() error {
	if d.eg == nil {
		return nil
	}

	d.srv.Go(func(c Context) {
		c.C() <- &dap.TerminatedEvent{
			Event: dap.Event{
				Event: "terminated",
			},
		}
		// TODO: detect exit code from threads
		// c.C() <- &dap.ExitedEvent{
		// 	Event: dap.Event{
		// 		Event: "exited",
		// 	},
		// 	Body: dap.ExitedEventBody{
		// 		ExitCode: exitCode,
		// 	},
		// }
	})
	d.srv.Stop()

	err := d.eg.Wait()
	d.eg = nil
	return err
}

func (d *Adapter) Initialize(c Context, req *dap.InitializeRequest, resp *dap.InitializeResponse) error {
	close(d.initialized)

	// Set capabilities.
	resp.Body.SupportsConfigurationDoneRequest = true
	return nil
}

func (d *Adapter) Launch(c Context, req *dap.LaunchRequest, resp *dap.LaunchResponse) error {
	d.start(c)
	return nil
}

func (d *Adapter) Attach(c Context, req *dap.AttachRequest, resp *dap.AttachResponse) error {
	d.start(c)
	return nil
}

func (d *Adapter) Disconnect(c Context, req *dap.DisconnectRequest, resp *dap.DisconnectResponse) error {
	close(d.evaluateReqCh)
	return nil
}

func (d *Adapter) start(c Context) {
	close(d.started)
	c.Go(d.launch)
}

func (d *Adapter) Continue(c Context, req *dap.ContinueRequest, resp *dap.ContinueResponse) error {
	d.threadsMu.RLock()
	t := d.threads[req.Arguments.ThreadId]
	d.threadsMu.RUnlock()

	t.Resume(c)
	return nil
}

func (d *Adapter) SetBreakpoints(c Context, req *dap.SetBreakpointsRequest, resp *dap.SetBreakpointsResponse) error {
	return nil
}

func (d *Adapter) ConfigurationDone(c Context, req *dap.ConfigurationDoneRequest, resp *dap.ConfigurationDoneResponse) error {
	d.configuration <- struct{}{}
	close(d.configuration)
	return nil
}

func (d *Adapter) Evaluate(c Context, req *dap.EvaluateRequest, resp *dap.EvaluateResponse) error {
	if req.Arguments.Context != "repl" {
		return errors.New("unsupported context")
	}

	args, err := shlex.Split(req.Arguments.Expression)
	if err != nil {
		return err
	} else if len(args) == 0 {
		return nil
	}

	switch arg0 := args[0]; arg0 {
	case "exec":
	}
	return nil
}

func (d *Adapter) launch(c Context) {
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
			started := c.Go(func(c Context) {
				defer d.deleteThread(c, t)
				defer close(req.errCh)
				req.errCh <- t.Evaluate(c, req.c, req.res)
			})

			if !started {
				req.errCh <- context.Canceled
				close(req.errCh)
			}
		}
	}
}

func (d *Adapter) newThread(ctx Context) (t *thread) {
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

	ctx.C() <- &dap.ThreadEvent{
		Event: dap.Event{Event: "thread"},
		Body: dap.ThreadEventBody{
			Reason:   "started",
			ThreadId: t.id,
		},
	}
	return t
}

func (d *Adapter) deleteThread(ctx Context, t *thread) {
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

func (d *Adapter) EvaluateResult(ctx context.Context, c gateway.Client, res *gateway.Result) error {
	errCh := make(chan error, 1)

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

func (d *Adapter) Threads(c Context, req *dap.ThreadsRequest, resp *dap.ThreadsResponse) error {
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

func (d *Adapter) Handler() build.Handler {
	return build.Handler{
		Evaluate: func(ctx context.Context, c gateway.Client, res *gateway.Result) error {
			errCh := make(chan error, 1)

			started := d.srv.Go(func(ctx Context) {
				defer close(errCh)
				errCh <- d.EvaluateResult(ctx, c, res)
			})
			if !started {
				return context.Canceled
			}

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (d *Adapter) dapHandler() Handler {
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

type thread struct {
	d    *Adapter
	id   int
	name string

	paused chan struct{}
	req    *containerRequest
	mu     sync.Mutex
}

func (t *thread) Evaluate(ctx Context, c gateway.Client, res *gateway.Result) error {
	return res.EachRef(func(ref gateway.Reference) error {
		if err := ref.Evaluate(ctx); err != nil && t.d.cfg.SuspendOn.OnError() {
			var solveErr errdefs.SolveError
			if errors.As(err, &solveErr) {
				paused, err := t.containerConfigFromError(ctx, &solveErr)
				if err != nil {
					return err
				}

				select {
				case <-paused:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return err
		}

		if t.d.cfg.SuspendOn == SuspendAlways {
			paused, err := t.containerConfigFromResult(ctx, res)
			if err != nil {
				return err
			}
			select {
			case <-paused:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
}

func (t *thread) containerConfigFromResult(c Context, res *gateway.Result) (<-chan struct{}, error) {
	ps, err := exptypes.ParsePlatforms(res.Metadata)
	if err != nil {
		return nil, err
	}

	ref, ok := res.FindRef(ps.Platforms[0].ID)
	if !ok {
		return nil, errors.Errorf("no reference found")
	}

	req := containerRequest{
		NewContainer: gateway.NewContainerRequest{
			Mounts: []gateway.Mount{
				{
					Dest:      "/",
					MountType: pb.MountType_BIND,
					Ref:       ref,
				},
			},
		},
	}

	imgData := res.Metadata[exptypes.ExporterImageConfigKey]
	var img *ocispecs.Image
	if len(imgData) > 0 {
		img = &ocispecs.Image{}
		if err := json.Unmarshal(imgData, img); err != nil {
			return nil, err
		}
	}

	if img != nil {
		req.Start.User = img.Config.User
		req.Start.Cwd = img.Config.WorkingDir
		req.Start.Env = img.Config.Env

		req.Start.Args = append([]string{}, img.Config.Entrypoint...)
		// Store the command separately so we can add it before
		// executing the container.
		req.Cmd = append([]string{}, img.Config.Cmd...)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Store our request.
	t.req = &req

	paused := t.pause(c, "pause", "Result built")
	return paused, nil
}

func (t *thread) containerConfigFromError(c Context, solveErr *errdefs.SolveError) (<-chan struct{}, error) {
	exec, err := execOpFromError(solveErr)
	if err != nil {
		return nil, err
	}

	var mounts []gateway.Mount
	for i, mnt := range exec.Mounts {
		rid := solveErr.MountIDs[i]
		mounts = append(mounts, gateway.Mount{
			Selector:  mnt.Selector,
			Dest:      mnt.Dest,
			ResultID:  rid,
			Readonly:  mnt.Readonly,
			MountType: mnt.MountType,
			CacheOpt:  mnt.CacheOpt,
			SecretOpt: mnt.SecretOpt,
			SSHOpt:    mnt.SSHOpt,
		})
	}

	req := containerRequest{
		NewContainer: gateway.NewContainerRequest{
			Mounts:  mounts,
			NetMode: exec.Network,
		},
	}

	req.Start.User = exec.Meta.User
	req.Start.Cwd = exec.Meta.Cwd
	req.Start.Env = exec.Meta.Env

	t.mu.Lock()
	defer t.mu.Unlock()

	// Store our request.
	t.req = &req

	paused := t.pause(c, "exception", "Encountered an error during build")
	return paused, nil
}

func execOpFromError(solveErr *errdefs.SolveError) (*pb.ExecOp, error) {
	if solveErr == nil {
		return nil, errors.Errorf("no error is available")
	}
	switch op := solveErr.Op.GetOp().(type) {
	case *pb.Op_Exec:
		return op.Exec, nil
	default:
		return nil, errors.Errorf("invoke: unsupported error type")
	}
	// TODO: support other ops
}

func (t *thread) pause(c Context, reason, desc string) <-chan struct{} {
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

func (t *thread) Resume(c Context) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.paused == nil {
		return
	}

	close(t.paused)
	t.paused = nil
}

func (t *thread) Exec(ctx Context, c gateway.Client, args []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.paused == nil {
		return errors.Errorf("thread %d cannot spawn a process since it is not paused", t.id)
	}

	if t.req == nil {
		return errors.Errorf("thread %d cannot spawn a process because there is no container to execute", t.id)
	}

	req := *t.req

	ctr, err := c.NewContainer(ctx, req.NewContainer)
	if err != nil {
		return err
	}

	if len(args) > 0 {
		req.Start.Args = append(req.Start.Args, args...)
	} else if len(req.Cmd) > 0 {
		req.Start.Args = append(req.Start.Args, req.Cmd...)
	}
	return nil
}

type containerRequest struct {
	NewContainer gateway.NewContainerRequest
	Start        gateway.StartRequest
	Cmd          []string
}
