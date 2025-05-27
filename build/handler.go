package build

import (
	"context"

	gateway "github.com/moby/buildkit/frontend/gateway/client"
)

type (
	ResultFunc   func(driverIndex int, rCtx *ResultHandle)
	EvaluateFunc func(ctx context.Context, c gateway.Client, res *gateway.Result) error
)

type Handler struct {
	OnResult ResultFunc
	Evaluate EvaluateFunc
}
