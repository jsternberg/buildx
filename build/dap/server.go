package dap

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/google/go-dap"
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

func (s *Server) Serve(ctx context.Context, rdwr io.ReadWriter) error {
	writeCh := make(chan dap.Message)
	s.ch = writeCh

	var cctx context.Context
	cctx, s.cancel = context.WithCancelCause(ctx)

	// Start an error group to handle server-initiated tasks.
	s.eg, s.ctx = errgroup.WithContext(cctx)
	s.eg.Go(func() error {
		<-cctx.Done()
		return cctx.Err()
	})

	// read loop runs as an orphan goroutine. If it fails, it will
	// signal for the other goroutines to exit, but this function
	// is allowed to return without read loop returning. This is because
	// the reader may not close until the program exits (such as from stdin).
	go s.readLoop(rdwr, s.cancel)

	eg, _ := errgroup.WithContext(cctx)
	eg.Go(func() error {
		return s.writeLoop(rdwr, writeCh)
	})

	eg.Go(func() error {
		defer close(writeCh)
		return s.eg.Wait()
	})
	return eg.Wait()
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
		s:       s,
		ch:      ch,
	}

	s.eg.Go(func() error {
		defer cancel()
		fn(c)
		return nil
	})
	return true
}

func (s *Server) readLoop(rd io.Reader, cancel func(err error)) {
	var err error
	defer func() { cancel(err) }()

	brd := bufio.NewReader(rd)
	for {
		var m dap.Message
		m, err = dap.ReadProtocolMessage(brd)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return
		}

		switch m := m.(type) {
		case dap.RequestMessage:
			if ok := s.dispatch(m); !ok {
				return
			}
		}
	}
}

func (s *Server) dispatch(m dap.RequestMessage) bool {
	return s.Go(func(c Context) {
		rmsg, err := s.handleMessage(c, m)
		if err != nil {
			rmsg = &dap.Response{}
			rmsg.GetResponse().Message = err.Error()
		}
		rmsg.GetResponse().RequestSeq = m.GetSeq()
		rmsg.GetResponse().Command = m.GetRequest().Command
		rmsg.GetResponse().Success = err == nil
		c.C() <- rmsg
	})
}

func (s *Server) writeLoop(w io.Writer, respCh <-chan dap.Message) error {
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

		if err := dap.WriteProtocolMessage(w, m); err != nil {
			return err
		}
	}
	return nil
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

func (s *Server) Stop() {
	s.mu.Lock()
	s.ch = nil
	s.mu.Unlock()
	s.cancel(nil)
}
