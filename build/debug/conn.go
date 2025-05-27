package debug

import (
	"bufio"
	"context"
	"io"
	"sync"

	"github.com/google/go-dap"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type Conn interface {
	SendMsg(m dap.Message) error
	RecvMsg(ctx context.Context) (dap.Message, error)
	io.Closer
}

type conn struct {
	recvCh <-chan dap.Message
	sendCh chan<- dap.Message

	ctx    context.Context
	cancel func()
}

func Pipe() (Conn, Conn) {
	ch1 := make(chan dap.Message, 100)
	ch2 := make(chan dap.Message, 100)

	conn1 := &conn{
		recvCh: ch1,
		sendCh: ch2,
	}
	conn2 := &conn{
		recvCh: ch2,
		sendCh: ch1,
	}
	return conn1, conn2
}

func (c *conn) SendMsg(m dap.Message) error {
	select {
	case c.sendCh <- m:
		return nil
	default:
		return errors.New("send channel full")
	}
}

func (c *conn) RecvMsg(ctx context.Context) (dap.Message, error) {
	select {
	case m := <-c.recvCh:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

func (c *conn) Close() error {
	c.cancel()
	return nil
}

type ioConn struct {
	conn
	eg   *errgroup.Group
	once sync.Once
}

func (c *ioConn) Close() error {
	c.cancel()
	c.once.Do(func() {
		close(c.sendCh)
	})
	return c.eg.Wait()
}

func IoConn(rdwr io.ReadWriter) Conn {
	recvCh := make(chan dap.Message, 100)
	sendCh := make(chan dap.Message, 100)
	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)
		defer close(recvCh)

		rd := bufio.NewReader(rdwr)
		for {
			m, err := dap.ReadProtocolMessage(rd)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
			recvCh <- m
		}
	}()

	eg, _ := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		for m := range sendCh {
			if err := dap.WriteProtocolMessage(rdwr, m); err != nil {
				return err
			}
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	return &ioConn{
		conn: conn{
			recvCh: recvCh,
			sendCh: sendCh,
			ctx:    ctx,
			cancel: cancel,
		},
		eg: eg,
	}
}
