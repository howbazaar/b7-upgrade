package main

import (
	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/juju/state"
	"github.com/juju/utils"
)

func (c *upgrade) upgradeDB(ctx *cmd.Context) error {
	if len(c.args) > 0 {
		return errors.Errorf("unexpected args: %v", c.args)
	}
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

	_ = models

	// TODO: the first thing that should happen is to create a controller-uuid
	// This needs to be stored both in the DB and the agent config.

	controllerUUID := utils.MustNewUUID()
	ctx.Infof("Generated new controller UUID: %s", controllerUUID.String())

	// TODO: sanity check that lists all collections in the DB and verifies
	// that all collections that we don't migrate or haven't verified no-
	// change have no rows.

	if err := renameAdminModel(ctx, st, c.live); err != nil {
		return err
	}
	if err := updateDefaultModel(ctx, st, c.live); err != nil {
		return err
	}

	return errors.NotImplementedf("complete migration")
}

func renameAdminModel(ctx *cmd.Context, st *state.State, live bool) error {
	// Read "admin" model

	// Rename to "controller"

	return errors.NotImplementedf("complete migration")
}

func updateDefaultModel(ctx *cmd.Context, st *state.State, live bool) error {
	// Read
	return errors.NotImplementedf("complete migration")
}
