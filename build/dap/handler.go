package dap

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/go-dap"
)

type Context interface {
	context.Context
	C() chan<- dap.Message
	Go(f func(c Context)) bool
}

type dispatchContext struct {
	context.Context
	s  *Server
	ch chan<- dap.Message
}

func (c *dispatchContext) C() chan<- dap.Message {
	return c.ch
}

func (c *dispatchContext) Go(f func(c Context)) bool {
	return c.s.Go(f)
}

type HandlerFunc[Req dap.RequestMessage, Resp dap.ResponseMessage] func(c Context, req Req, resp Resp) error

func (h HandlerFunc[Req, Resp]) Do(c Context, req Req) (resp Resp, err error) {
	if h == nil {
		return resp, errors.New("not implemented")
	}

	respT := reflect.TypeFor[Resp]()
	rv := reflect.New(respT.Elem())
	resp = rv.Interface().(Resp)
	err = h(c, req, resp)
	return resp, err
}

type Handler struct {
	Initialize        HandlerFunc[*dap.InitializeRequest, *dap.InitializeResponse]
	Launch            HandlerFunc[*dap.LaunchRequest, *dap.LaunchResponse]
	Attach            HandlerFunc[*dap.AttachRequest, *dap.AttachResponse]
	Disconnect        HandlerFunc[*dap.DisconnectRequest, *dap.DisconnectResponse]
	Terminate         HandlerFunc[*dap.TerminateRequest, *dap.TerminateResponse]
	Restart           HandlerFunc[*dap.RestartRequest, *dap.RestartResponse]
	SetBreakpoints    HandlerFunc[*dap.SetBreakpointsRequest, *dap.SetBreakpointsResponse]
	ConfigurationDone HandlerFunc[*dap.ConfigurationDoneRequest, *dap.ConfigurationDoneResponse]
}
