// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"errors"

	"github.com/juju/cmd"
	"github.com/juju/gnuflag"
	"github.com/juju/loggo"
)

var logger = loggo.GetLogger("juju.b7-upgrade")

type upgrade struct {
	live   bool
	action string
}

const helpDoc = `
Upgrades a Juju 2.0-beta7 controller and models to 2.0.
`

// Info implements Command.
func (c *upgrade) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "b7-upgrade",
		Args:    "[client|server]",
		Purpose: "Upgrade a beta 7 controller",
		Doc:     helpDoc,
	}
}

func (c *upgrade) IsSuperCommand() bool {
	return false
}

func (c *upgrade) AllowInterspersedFlags() bool {
	return false
}

// SetFlags implements Command.
func (c *upgrade) SetFlags(f *gnuflag.FlagSet) {
	f.BoolVar(&c.live, "live", false, "Do for real, not just dry-run")
}

// Init implements Command.
func (c *upgrade) Init(args []string) error {
	if len(args) == 0 {
		return errors.New("missing action, options are 'client' or 'server'")
	}
	c.action, args = args[0], args[1:]
	switch c.action {
	case "client", "server":
	default:
		return errors.New("unknown action, options are 'client' or 'server'")
	}
	return cmd.CheckEmpty(args)
}

// Run implements Command.
func (c *upgrade) Run(ctx *cmd.Context) error {

	if c.live {
		logger.Warningf("Running LIVE")
	} else {
		logger.Infof("Running dry-run")
	}

	switch c.action {
	case "server":
		return server()
	case "client":
		return client()
	default:
		return errors.New("unknown action")
	}
}
