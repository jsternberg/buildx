package build

type (
	ResultFunc func(driverIndex int, rCtx *ResultHandle)
	StartFunc  func()
	ExitFunc   func(exitCode int)
)

type Handler struct {
	OnResult ResultFunc

	OnStart StartFunc
	OnExit  ExitFunc
}
