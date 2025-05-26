package build

import (
	"context"

	gateway "github.com/moby/buildkit/frontend/gateway/client"
)

type (
	ResultFunc func(driverIndex int, rCtx *ResultHandle)

	StartFunc    func()
	ExitFunc     func(exitCode int)
	EvaluateFunc func(ctx context.Context, c gateway.Client, res *gateway.Result) error
)

type Handler struct {
	OnResult ResultFunc

	OnStart StartFunc
	OnExit  ExitFunc

	Evaluate EvaluateFunc
}

// target a: [1, 2, 3]
// target b: [4, 5]
//
// buildx build --target a
// normal build - solve [1, 2, 3]
// debug build
// 		solve [1]
// 		solve [1, 2]
// 		solve [1, 2, 3]
