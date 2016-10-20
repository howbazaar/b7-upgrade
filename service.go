package main

import (
	"fmt"
	"strings"

	"github.com/juju/cmd"
	"github.com/juju/errors"
)

func (c *upgrade) agents(ctx *cmd.Context) error {
	if len(c.args) == 0 {
		return errors.Errorf("missing action: [status, stop, start-controller, start-others]")
	}
	if len(c.args) > 1 {
		return errors.Errorf("unexpected args: %v", c.args[1:])
	}

	switch c.args[0] {
	case "stop":
		return c.stopAgents(ctx)
	case "start-controller":
		return c.startServer(ctx)
	case "start-others":
		return c.startAgents(ctx)
	case "status":
		return c.agentStatus(ctx)
	default:
		return errors.Errorf("unknown action: %q", c.args[0])
	}
	return nil
}

func (c *upgrade) stopAgents(ctx *cmd.Context) error {
	machines, err := getAllMachines()
	if err != nil {
		return errors.Trace(err)
	}

	return serviceCall(ctx, machines, "stop")
}

func (c *upgrade) startServer(ctx *cmd.Context) error {
	server, err := getServerMachine()
	if err != nil {
		return errors.Trace(err)
	}
	return serviceCall(ctx, []FlatMachine{server}, "start")
}

func (c *upgrade) startAgents(ctx *cmd.Context) error {
	machines, err := getOtherMachines()
	if err != nil {
		return errors.Trace(err)
	}

	return serviceCall(ctx, machines, "start")
}

func (c *upgrade) agentStatus(ctx *cmd.Context) error {
	machines, err := getAllMachines()
	if err != nil {
		return errors.Trace(err)
	}

	return serviceCall(ctx, machines, "status")
}

func serviceCall(ctx *cmd.Context, machines []FlatMachine, command string) error {

	script := fmt.Sprintf(`
set -xu
cd /var/lib/juju/agents
for agent in *
do
	sudo service jujud-$agent %s
done
	`, command)

	results := parallelCall(machines, script)

	for _, result := range results {
		ctx.Infof("%s %s", result.Model, result.MachineID)
		if result.Error != nil {
			ctx.Infof("  ERROR: %v", result.Error)
		}
		if result.Code != 0 {
			ctx.Infof("  Code: %s", result.Code)
		}
		if result.Stdout != "" {
			out := strings.Join(strings.Split(result.Stdout, "\n"), "\n    ")
			ctx.Infof("    %s", out)
		}
		if result.Stderr != "" {
			logger.Debugf("%s/%s stderr: \n%s", result.Model, result.MachineID, result.Stderr)
		}
	}

	return nil
}
