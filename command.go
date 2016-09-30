// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"sort"
	"strings"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/gnuflag"
	"github.com/juju/loggo"
)

var logger = loggo.GetLogger("b7-upgrade")

type upgrade struct {
	live   bool
	action func(*cmd.Context) error

	debug  bool
	jdebug bool
	args   []string
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
	return true
}

// SetFlags implements Command.
func (c *upgrade) SetFlags(f *gnuflag.FlagSet) {
	f.BoolVar(&c.live, "live", false, "Do for real, not just dry-run")
	f.BoolVar(&c.debug, "debug", false, "Show debug logging")
	f.BoolVar(&c.jdebug, "jdebug", false, "Show juju debug logging")
}

// Init implements Command.
func (c *upgrade) Init(args []string) error {
	if len(args) == 0 {
		return errors.Errorf("missing action, options are: %s", c.validCommands())
	}
	var action string
	action, args = args[0], args[1:]
	if f, found := c.commands()[action]; !found {
		return errors.Errorf("unknown action, options are: %s", c.validCommands())
	} else {
		c.action = f
	}
	c.args = args
	return nil
}

func (c *upgrade) validCommands() string {
	var result []string
	for name := range c.commands() {
		result = append(result, name)
	}
	sort.Strings(result)
	return strings.Join(result, ", ")
}

func (c *upgrade) commands() map[string]func(ctx *cmd.Context) error {
	return map[string]func(ctx *cmd.Context) error{
		"verify-db":           c.verifyDB,
		"distribute-upgrader": c.distributeUpgrader,
		"client":              c.client,
		"agents":              c.agents,
		"upgrade-db":          c.upgradeDB,
	}
}

// Run implements Command.
func (c *upgrade) Run(ctx *cmd.Context) error {
	if c.debug {
		logger.SetLogLevel(loggo.DEBUG)
	}
	if c.jdebug {
		loggo.GetLogger("juju").SetLogLevel(loggo.DEBUG)
	}

	if c.live {
		ctx.Infof("Running LIVE")
	} else {
		ctx.Infof("Running dry-run")
	}

	return c.action(ctx)
}
