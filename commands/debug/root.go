package debug

import (
	"github.com/docker/buildx/build"
	"github.com/docker/buildx/util/cobrautil"
	"github.com/docker/buildx/util/progress"
	"github.com/docker/cli/cli/command"
	"github.com/spf13/cobra"
)

// DebuggableCmd is a command that supports debugger with recognizing the user-specified DebugConfig.
type DebuggableCmd interface {
	// WithDebugger returns the new *cobra.Command with support for the debugger with recognizing DebugConfig.
	WithDebugger(Debugger) *cobra.Command
}

// Debugger will start a debugger instance.
type Debugger interface {
	Start(dockerCli command.Cli, printer *progress.Printer) (DebuggerInstance, error)
}

// DebuggerInstance is an instance of a Debugger that has been started.
type DebuggerInstance interface {
	Handler() build.Handler
	Stop() error
}

func AddCommands(rootCmd *cobra.Command, dockerCli command.Cli, children ...DebuggableCmd) {
	rootCmd.AddCommand(RootCmd(dockerCli, children...))
	rootCmd.AddCommand(DapCmd(dockerCli, children...))
}

func RootCmd(dockerCli command.Cli, children ...DebuggableCmd) *cobra.Command {
	var debugger MonitorDebugger

	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Start debugger",
	}
	cobrautil.MarkCommandExperimental(cmd)

	flags := cmd.Flags()
	flags.StringVar(&debugger.InvokeFlag, "invoke", "", "Launch a monitor with executing specified command")
	flags.StringVar(&debugger.OnFlag, "on", "error", "When to launch the monitor ([always, error])")

	cobrautil.MarkFlagsExperimental(flags, "invoke", "on")

	for _, c := range children {
		cmd.AddCommand(c.WithDebugger(&debugger))
	}

	return cmd
}

func DapCmd(dockerCli command.Cli, children ...DebuggableCmd) *cobra.Command {
	var debugger AdapterProtocolDebugger

	cmd := &cobra.Command{
		Use:   "dap",
		Short: "Start debug adapter",
	}
	cobrautil.MarkCommandExperimental(cmd)

	for _, c := range children {
		cmd.AddCommand(c.WithDebugger(&debugger))
	}

	return cmd
}
