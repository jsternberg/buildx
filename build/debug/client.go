package debug

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/go-dap"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type Client struct {
	conn Conn

	requests   map[int]chan<- dap.ResponseMessage
	events     map[string]func(dap.EventMessage)
	requestsMu sync.Mutex

	seq    atomic.Int64
	eg     *errgroup.Group
	cancel func()
}

func NewClient(conn Conn) *Client {
	c := &Client{
		conn:     conn,
		requests: make(map[int]chan<- dap.ResponseMessage),
		events:   make(map[string]func(dap.EventMessage)),
	}

	var ctx context.Context
	ctx, c.cancel = context.WithCancel(context.Background())

	c.eg, _ = errgroup.WithContext(context.Background())
	c.eg.Go(func() error {
		for {
			m, err := conn.RecvMsg(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}

			switch m := m.(type) {
			case dap.RequestMessage:
				// TODO: no reverse requests are currently supported
				conn.SendMsg(&dap.Response{
					ProtocolMessage: dap.ProtocolMessage{
						Seq:  c.nextSeq(),
						Type: "reponse",
					},
					RequestSeq: m.GetRequest().GetSeq(),
					Success:    false,
					Command:    m.GetRequest().Command,
					Message:    "not implemented",
				})
			case dap.ResponseMessage:
				c.requestsMu.Lock()
				req := m.GetResponse().GetResponse().RequestSeq
				ch := c.requests[req]
				delete(c.requests, req)
				c.requestsMu.Unlock()

				if ch != nil {
					ch <- m
				}
			case dap.EventMessage:
				fn := c.events[m.GetEvent().Event]
				if fn != nil {
					fn(m)
				}
			}
		}
	})
	return c
}

func (c *Client) Do(req dap.RequestMessage) <-chan dap.ResponseMessage {
	req.GetRequest().Type = "request"
	req.GetRequest().Seq = c.nextSeq()

	ch := make(chan dap.ResponseMessage, 1)
	if err := c.conn.SendMsg(req); err != nil {
		ch <- &dap.Response{
			ProtocolMessage: dap.ProtocolMessage{
				Seq:  c.nextSeq(),
				Type: "response",
			},
			RequestSeq: req.GetRequest().GetSeq(),
			Success:    false,
			Command:    req.GetRequest().Command,
			Message:    err.Error(),
		}
		return ch
	}

	c.requestsMu.Lock()
	c.requests[req.GetSeq()] = ch
	c.requestsMu.Unlock()
	return ch
}

func (c *Client) RegisterEvent(event string, fn func(dap.EventMessage)) {
	c.events[event] = fn
}

func (c *Client) Close() error {
	c.cancel()
	return c.eg.Wait()
}

func (c *Client) nextSeq() int {
	seq := c.seq.Add(1)
	return int(seq)
}
