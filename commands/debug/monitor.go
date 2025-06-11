package debug

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/docker/buildx/build"
	"github.com/docker/buildx/monitor"
	"github.com/docker/buildx/util/progress"
	"github.com/docker/cli/cli/command"
	"github.com/pkg/errors"
	"github.com/tonistiigi/go-csvvalue"
)

type MonitorDebugger struct {
	// InvokeFlag is a flag to configure the launched debugger and the commaned executed on the debugger.
	InvokeFlag string

	// OnFlag is a flag to configure the timing of launching the debugger.
	OnFlag string
}

func (d *MonitorDebugger) Start(dockerCli command.Cli, printer *progress.Printer) (DebuggerInstance, error) {
	cfg, err := parseInvokeConfig(d.InvokeFlag, d.OnFlag)
	if err != nil {
		return nil, err
	}

	m := monitor.New(cfg, dockerCli.In(), os.Stdout, os.Stderr, printer)
	return &monitorDebuggerInstance{m: m}, nil
}

type monitorDebuggerInstance struct {
	m *monitor.Monitor
}

func (d *monitorDebuggerInstance) Handler() build.Handler {
	return d.m.Handler()
}

func (d *monitorDebuggerInstance) Stop() error {
	return d.m.Close()
}

func parseInvokeConfig(invoke, on string) (*build.InvokeConfig, error) {
	cfg := &build.InvokeConfig{}
	switch on {
	case "always":
		cfg.SuspendOn = build.SuspendAlways
	case "error":
		cfg.SuspendOn = build.SuspendError
	default:
		if invoke != "" {
			cfg.SuspendOn = build.SuspendAlways
		}
	}

	cfg.Tty = true
	cfg.NoCmd = true
	switch invoke {
	case "default", "":
		return cfg, nil
	case "on-error":
		// NOTE: we overwrite the command to run because the original one should fail on the failed step.
		// TODO: make this configurable via flags or restorable from LLB.
		// Discussion: https://github.com/docker/buildx/pull/1640#discussion_r1113295900
		cfg.Cmd = []string{"/bin/sh"}
		cfg.NoCmd = false
		return cfg, nil
	}

	csvParser := csvvalue.NewParser()
	csvParser.LazyQuotes = true
	fields, err := csvParser.Fields(invoke, nil)
	if err != nil {
		return nil, err
	}
	if len(fields) == 1 && !strings.Contains(fields[0], "=") {
		cfg.Cmd = []string{fields[0]}
		cfg.NoCmd = false
		return cfg, nil
	}
	cfg.NoUser = true
	cfg.NoCwd = true
	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			return nil, errors.Errorf("invalid value %s", field)
		}
		key := strings.ToLower(parts[0])
		value := parts[1]
		switch key {
		case "args":
			cfg.Cmd = append(cfg.Cmd, maybeJSONArray(value)...)
			cfg.NoCmd = false
		case "entrypoint":
			cfg.Entrypoint = append(cfg.Entrypoint, maybeJSONArray(value)...)
			if cfg.Cmd == nil {
				cfg.Cmd = []string{}
				cfg.NoCmd = false
			}
		case "env":
			cfg.Env = append(cfg.Env, maybeJSONArray(value)...)
		case "user":
			cfg.User = value
			cfg.NoUser = false
		case "cwd":
			cfg.Cwd = value
			cfg.NoCwd = false
		case "tty":
			cfg.Tty, err = strconv.ParseBool(value)
			if err != nil {
				return nil, errors.Errorf("failed to parse tty: %v", err)
			}
		default:
			return nil, errors.Errorf("unknown key %q", key)
		}
	}
	return cfg, nil
}

func maybeJSONArray(v string) []string {
	var list []string
	if err := json.Unmarshal([]byte(v), &list); err == nil {
		return list
	}
	return []string{v}
}
