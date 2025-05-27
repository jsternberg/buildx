package debug

import (
	"context"
	"sync"

	"github.com/docker/buildx/build"
	"github.com/google/go-dap"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type Server struct {
	h Handler

	mu sync.RWMutex
	ch chan dap.Message

	eg     *errgroup.Group
	ctx    context.Context
	cancel context.CancelCauseFunc

	initialized bool
}

func NewServer(h Handler) *Server {
	return &Server{h: h}
}

func (a *Server) Serve(ctx context.Context, conn Conn) error {
	writeCh := make(chan dap.Message)
	a.ch = writeCh

	a.ctx, a.cancel = context.WithCancelCause(ctx)

	// Start an error group to handle server-initiated tasks.
	a.eg, _ = errgroup.WithContext(a.ctx)
	a.eg.Go(func() error {
		<-a.ctx.Done()
		return a.ctx.Err()
	})

	eg, _ := errgroup.WithContext(a.ctx)
	eg.Go(func() error {
		return a.readLoop(conn, eg)
	})

	eg.Go(func() error {
		return a.writeLoop(conn, writeCh)
	})

	eg.Go(func() error {
		defer close(writeCh)
		return a.eg.Wait()
	})

	return eg.Wait()
}

func (a *Server) readLoop(conn Conn, eg *errgroup.Group) error {
	for {
		m, err := conn.RecvMsg(a.ctx)
		if err != nil {
			return nil
		}

		switch m := m.(type) {
		case dap.RequestMessage:
			if ok := a.dispatch(m); !ok {
				return nil
			}
		}
	}
}

func (a *Server) dispatch(m dap.RequestMessage) bool {
	fn := func(c Context) {
		rmsg, err := a.handleMessage(c, m)
		if err != nil {
			rmsg = &dap.Response{}
			rmsg.GetResponse().Message = err.Error()
		}
		rmsg.GetResponse().RequestSeq = m.GetSeq()
		rmsg.GetResponse().Command = m.GetRequest().Command
		rmsg.GetResponse().Success = err == nil
		c.C() <- rmsg
	}
	return a.Go(fn)
}

func (s *Server) handleMessage(c Context, m dap.Message) (dap.ResponseMessage, error) {
	switch req := m.(type) {
	case *dap.InitializeRequest:
		resp, err := s.handleInitialize(c, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case *dap.LaunchRequest:
		return s.h.Launch.Do(c, req)
	case *dap.AttachRequest:
		return s.h.Attach.Do(c, req)
	case *dap.ContinueRequest:
		return s.h.Continue.Do(c, req)
	default:
		return nil, errors.New("not implemented")
	}
}

func (s *Server) handleInitialize(c Context, req *dap.InitializeRequest) (*dap.InitializeResponse, error) {
	if s.initialized {
		return nil, errors.New("already initialized")
	}

	resp, err := s.h.Initialize.Do(c, req)
	if err != nil {
		return nil, err
	}
	s.initialized = true
	return resp, nil
}

func (s *Server) writeLoop(conn Conn, respCh <-chan dap.Message) error {
	var seq int
	for m := range respCh {
		switch m := m.(type) {
		case dap.RequestMessage:
			m.GetRequest().Seq = seq
			m.GetRequest().Type = "request"
		case dap.EventMessage:
			m.GetEvent().Seq = seq
			m.GetEvent().Type = "event"
		case dap.ResponseMessage:
			m.GetResponse().Seq = seq
			m.GetResponse().Type = "response"
		}
		seq++

		if err := conn.SendMsg(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) Go(fn func(c Context)) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ch := s.ch
	if ch == nil {
		return false
	}

	ctx, cancel := context.WithCancel(s.ctx)
	c := &dispatchContext{
		Context: ctx,
		srv:     s,
		ch:      ch,
	}

	s.eg.Go(func() error {
		defer cancel()
		fn(c)
		return nil
	})
	return true
}

func (a *Server) Stop() {
	a.mu.Lock()
	a.ch = nil
	a.mu.Unlock()
	a.cancel(nil)
}

func (a *Server) Handler() build.Handler {
	return build.Handler{}
}
