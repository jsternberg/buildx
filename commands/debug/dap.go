package debug

import (
	"bufio"
	"context"
	"io"

	"github.com/containerd/console"
	"github.com/docker/buildx/dap"
	"github.com/docker/buildx/util/ioset"
	"github.com/docker/buildx/util/progress"
	"github.com/pkg/errors"
)

type AdapterProtocolDebugger struct{}

func (d *AdapterProtocolDebugger) New(in ioset.In) (DebuggerInstance, error) {
	conn := dap.IoConn(readWriter{
		Reader: in.Stdin,
		Writer: in.Stdout,
	})

	adapter := dap.New()
	return &adapterProtocolDebuggerInstance{
		Adapter: adapter,
		conn:    conn,
	}, nil
}

type adapterProtocolDebuggerInstance struct {
	*dap.Adapter

	conn dap.Conn
}

func (d *adapterProtocolDebuggerInstance) Start(printer *progress.Printer) error {
	if err := d.Adapter.Start(context.Background(), d.conn); err != nil {
		return errors.Wrap(err, "debug adapter did not start")
	}
	return nil
}

func (d *adapterProtocolDebuggerInstance) Out() console.File {
	w := bufio.NewWriter(d.Adapter.Out())
	return fakeConsole{Writer: w}
}

type readWriter struct {
	io.Reader
	io.Writer
}

type fakeConsole struct {
	io.Writer
}

func (fakeConsole) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (fakeConsole) Close() error {
	return nil
}

func (fakeConsole) Fd() uintptr {
	return 0
}

func (fakeConsole) Name() string {
	return ""
}
