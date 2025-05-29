package debug

import (
	"github.com/google/go-dap"
)

type Client struct{}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Do(req dap.RequestMessage) <-chan dap.ResponseMessage {
	return nil
}

func (c *Client) RegisterEvent(event string, fn func(dap.EventMessage)) {
}

func (c *Client) Close() error {
	return nil
}
