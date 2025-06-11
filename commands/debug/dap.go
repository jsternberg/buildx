package debug

import (
	"context"
	"io"

	"github.com/docker/buildx/dap"
	"github.com/docker/buildx/util/progress"
	"github.com/docker/cli/cli/command"
	"github.com/pkg/errors"
)

type AdapterProtocolDebugger struct{}

func (d *AdapterProtocolDebugger) Start(dockerCli command.Cli, printer *progress.Printer) (DebuggerInstance, error) {
	conn := dap.IoConn(readWriter{
		Reader: dockerCli.In(),
		Writer: dockerCli.Out(),
	})

	adapter := dap.New()
	if err := adapter.Start(context.Background(), conn); err != nil {
		return nil, errors.Wrap(err, "debug adapter did not start")
	}
	return adapter, nil
}

type readWriter struct {
	io.Reader
	io.Writer
}
