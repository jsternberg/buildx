package build

import (
	"context"

	gateway "github.com/moby/buildkit/frontend/gateway/client"
)

type (
	ResultFunc func(driverIndex int, rCtx *ResultHandle)

	StartFunc func()
	ExitFunc  func(exitCode int)
	SolveFunc func(ctx context.Context, c gateway.Client, req gateway.SolveRequest) (*gateway.Result, error)
)

type Handler struct {
	OnResult ResultFunc

	OnStart StartFunc
	OnExit  ExitFunc

	Solve    SolveFunc
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
