package build

import (
	"context"

	gateway "github.com/moby/buildkit/frontend/gateway/client"
)

type (
	EvaluateFunc func(ctx context.Context, c gateway.Client, res *gateway.Result) error
)

type Handler struct {
	Evaluate EvaluateFunc
}
