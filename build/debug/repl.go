package debug

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/docker/buildx/monitor/types"
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
	t                  *term.Terminal
	registeredCommands map[string]types.Command
}

func NewTerminal(rdwr io.ReadWriter, prompt string) *Terminal {
	t := term.NewTerminal(rdwr, prompt)
	return &Terminal{t: t}
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
		return t.repl(ctx)
	})

	return eg.Wait()
}

func (t *Terminal) repl(ctx context.Context) error {
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)

	defer close(lineCh)
	defer close(errCh)

	for {
		// Read line may potentially never return so we invoke it in a goroutine
		// that can be orphaned if needed.
		go func() {
			l, err := t.t.ReadLine()
			if err != nil {
				errCh <- err
				return
			}
			lineCh <- l
		}()

		select {
		case l := <-lineCh:
			if exit := t.invoke(ctx, l); exit {
				return nil
			}
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *Terminal) invoke(ctx context.Context, l string) (exit bool) {
	args, err := shlex.Split(l)
	if err != nil {
		fmt.Fprintf(t.t, "monitor: failed to parse command: %v\n", err)
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
		return true
	case "help":
		if len(args) >= 2 {
			t.printHelpMessageOfCommand(args[1])
			return
		}
		t.printHelpMessage()
		return
	default:
	}

	// Registered commands
	cmd := args[0]
	if cm, ok := t.registeredCommands[cmd]; ok {
		if err := cm.Exec(ctx, args); err != nil {
			fmt.Fprintf(t.t, "%s: %v\n", cmd, err)
		}
	} else {
		fmt.Fprintf(t.t, "monitor: unknown command: %q\n", l)
		t.printHelpMessage()
	}
	return
}

func (t *Terminal) printHelpMessageOfCommand(name string) {
	var target types.Command
	if c, ok := t.registeredCommands[name]; ok {
		target = c
	} else {
		fmt.Fprintf(t.t, "monitor: no help message for %q\n", name)
		t.printHelpMessage()
		return
	}
	fmt.Fprintln(t.t, target.Info().HelpMessage)
	if h := target.Info().HelpMessageLong; h != "" {
		fmt.Fprintln(t.t, h)
	}
}

func (t *Terminal) printHelpMessage() {
	var names []string
	for name := range t.registeredCommands {
		names = append(names, name)
	}
	for name := range additionalHelpMessages {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprint(t.t, "Available commands are:\n")
	w := new(tabwriter.Writer)
	w.Init(t.t, 0, 8, 0, '\t', 0)
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
