package main

import (
	"github.com/juju/cmd"
	"github.com/juju/errors"
)

func verifyDB(ctx *cmd.Context, live bool) error {
	// Try to read the agent config file.
	// assuming machine-0 of controller

	st, err := openBeta7DB()
	if err != nil {
		return errors.Trace(err)
	}
	defer st.Close()

	models, err := getMachines(st)
	if err != nil {
		return errors.Trace(err)
	}

	ctx.Infof("Models and Machines:")
	for _, model := range models {
		ctx.Infof("%s (%s)", model.Name, model.UUID)
		for _, machine := range model.Machines {
			ctx.Infof("  %s: %s", machine.Tag.Id(), machine.Address)
		}
	}

	return nil
}
