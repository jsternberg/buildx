package debug

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"text/tabwriter"

	"github.com/docker/buildx/monitor/types"
	"github.com/docker/buildx/util/progress"
	"github.com/docker/cli/cli/command"
	"github.com/google/go-dap"
	"github.com/google/shlex"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

var additionalHelpMessages = map[string]string{
	"help": "shows this message. Optionally pass a command name as an argument to print the detailed usage.",
	"exit": "exits monitor",
}

type Terminal struct {
	dockerCli          command.Cli
	prompt             string
	printer            *progress.Printer
	registeredCommands map[string]types.Command

	paused   chan struct{}
	pausedMu sync.Mutex
}

func NewTerminal(dockerCli command.Cli, prompt string, printer *progress.Printer) *Terminal {
	return &Terminal{
		dockerCli: dockerCli,
		prompt:    prompt,
		printer:   printer,
	}
}

func (t *Terminal) Run(ctx context.Context, conn Conn) error {
	client := NewClient(conn)
	defer client.Close()

	eg, _ := errgroup.WithContext(ctx)

	client.RegisterEvent("initialized", func(_ dap.EventMessage) {
		eg.Go(func() error {
			// Wait for the initialized event and send configuration done.
			// We don't perform any additional configuration.
			select {
			case res := <-client.Do(&dap.ConfigurationDoneRequest{}):
				if !res.GetResponse().Success {
					return errors.New(res.GetResponse().Message)
				}
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	})

	t.paused = make(chan struct{})
	client.RegisterEvent("stopped", func(_ dap.EventMessage) {
		t.pausedMu.Lock()
		if t.paused != nil {
			close(t.paused)
		}
		t.paused = nil
		t.pausedMu.Unlock()
	})

	resCh := client.Do(&dap.InitializeRequest{})
	select {
	case res := <-resCh:
		if !res.GetResponse().Success {
			return errors.New(res.GetResponse().Message)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	resCh = client.Do(&dap.LaunchRequest{})
	select {
	case res := <-resCh:
		if !res.GetResponse().Success {
			return errors.New(res.GetResponse().Message)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	eg.Go(func() error {
		return t.run(ctx)
	})

	return eg.Wait()
}

func (t *Terminal) run(ctx context.Context) error {
	for {
		var paused <-chan struct{}

		t.pausedMu.Lock()
		if t.paused != nil {
			paused = t.paused
		}
		t.pausedMu.Unlock()

		select {
		case <-paused:
		case <-ctx.Done():
			return ctx.Err()
		}

		if err := t.repl(ctx); err != nil {
			return err
		}
	}
}

func (t *Terminal) repl(ctx context.Context) error {
	if err := t.printer.Pause(); err != nil {
		return err
	}
	defer t.printer.Unpause()

	in := t.dockerCli.In()
	out := t.dockerCli.Out()

	if err := in.SetRawTerminal(); err != nil {
		return err
	}

	defer in.RestoreTerminal()

	rdwr := readWriter{
		Reader: in,
		Writer: out,
	}

	prompt := term.NewTerminal(rdwr, t.prompt)

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)

	defer close(lineCh)
	defer close(errCh)

	for {
		// Read line may potentially never return so we invoke it in a goroutine
		// that can be orphaned if needed.
		go func() {
			l, err := prompt.ReadLine()
			if err != nil {
				errCh <- err
				return
			}
			lineCh <- l
		}()

		select {
		case l := <-lineCh:
			if resume, err := t.invoke(ctx, t.dockerCli.Out(), l); resume || err != nil {
				return err
			}
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *Terminal) invoke(ctx context.Context, out io.Writer, l string) (resume bool, err error) {
	args, err := shlex.Split(l)
	if err != nil {
		fmt.Fprintf(out, "monitor: failed to parse command: %v\n", err)
		return
	} else if len(args) == 0 {
		return
	}

	// Builtin commands
	switch args[0] {
	case "":
		// nop
		return
	case "exit":
		return true, io.EOF
	case "help":
		if len(args) >= 2 {
			t.printHelpMessageOfCommand(out, args[1])
			return
		}
		t.printHelpMessage(out)
		return
	default:
	}

	// Registered commands
	cmd := args[0]
	if cm, ok := t.registeredCommands[cmd]; ok {
		if err := cm.Exec(ctx, args); err != nil {
			fmt.Fprintf(out, "%s: %v\n", cmd, err)
		}
	} else {
		fmt.Fprintf(out, "monitor: unknown command: %q\n", l)
		t.printHelpMessage(out)
	}
	return
}

func (t *Terminal) printHelpMessageOfCommand(out io.Writer, name string) {
	var target types.Command
	if c, ok := t.registeredCommands[name]; ok {
		target = c
	} else {
		fmt.Fprintf(out, "monitor: no help message for %q\n", name)
		t.printHelpMessage(out)
		return
	}
	fmt.Fprintln(out, target.Info().HelpMessage)
	if h := target.Info().HelpMessageLong; h != "" {
		fmt.Fprintln(out, h)
	}
}

func (t *Terminal) printHelpMessage(out io.Writer) {
	var names []string
	for name := range t.registeredCommands {
		names = append(names, name)
	}
	for name := range additionalHelpMessages {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprint(out, "Available commands are:\n")
	w := new(tabwriter.Writer)
	w.Init(out, 0, 8, 0, '\t', 0)
	for _, name := range names {
		var mes string
		if c, ok := t.registeredCommands[name]; ok {
			mes = c.Info().HelpMessage
		} else if m, ok := additionalHelpMessages[name]; ok {
			mes = m
		} else {
			continue
		}
		fmt.Fprintln(w, "  "+name+"\t"+mes)
	}
	w.Flush()
}

type readWriter struct {
	io.Reader
	io.Writer
}
