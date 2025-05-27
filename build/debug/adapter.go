package debug

import "github.com/docker/buildx/build"

type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Start(conn Conn) {
}

func (a *Adapter) Stop() {
}

func (a *Adapter) Handler() build.Handler {
	return build.Handler{}
}
