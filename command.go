// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"github.com/juju/cmd"
	"github.com/juju/gnuflag"
	"github.com/juju/loggo"
)

var logger = loggo.GetLogger("juju.b7-upgrade")

type upgrade struct {
	live bool
}

const helpDoc = `
Upgrades a Juju 2.0-beta7 controller and models to 2.0.
`

// Info implements Command.
func (c *upgrade) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "b7-upgrade",
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
	return cmd.CheckEmpty(args)
}

// Run implements Command.
func (c *upgrade) Run(ctx *cmd.Context) error {

	if c.live {
		logger.Warningf("Running LIVE")
	} else {
		logger.Infof("Running dry-run")
	}

	return nil
}
