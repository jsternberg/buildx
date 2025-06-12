package dap

import (
	"io"

	"github.com/google/go-dap"
)

func (d *Adapter) Out() io.Writer {
	return &adapterWriter{d}
}

type adapterWriter struct {
	*Adapter
}

func (d *adapterWriter) Write(p []byte) (n int, err error) {
	started := d.srv.Go(func(c Context) {
		c.C() <- &dap.OutputEvent{
			Event: dap.Event{Event: "output"},
			Body: dap.OutputEventBody{
				Category: "stdout",
				Output:   string(p),
			},
		}
	})
	if !started {
		return 0, io.ErrClosedPipe
	}
	return n, nil
}
