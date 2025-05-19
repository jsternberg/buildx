package debug

type SuspendOnBehavior int

const (
	SuspendError SuspendOnBehavior = iota
	SuspendAlways
	SuspendNever
)

type Config struct {
	SuspendOn SuspendOnBehavior
}

func (s SuspendOnBehavior) OnError() bool {
	return s == SuspendError || s == SuspendAlways
}
