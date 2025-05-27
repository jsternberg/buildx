package dap

import (
	"bufio"
	"io"

	"github.com/google/go-dap"
)

type IO interface {
	SendMsg(m dap.Message) error
	RecvMsg() (dap.Message, error)
}

func ReadWriter(rdwr io.ReadWriter) IO {
	return &readWriter{
		rd: bufio.NewReader(rdwr),
		w:  rdwr,
	}
}

type readWriter struct {
	rd *bufio.Reader
	w  io.Writer
}

func (rw *readWriter) SendMsg(m dap.Message) error {
	return dap.WriteProtocolMessage(rw.w, m)
}

func (rw *readWriter) RecvMsg() (dap.Message, error) {
	return dap.ReadProtocolMessage(rw.rd)
}
