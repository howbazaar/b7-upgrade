package main

import (
	"github.com/juju/cmd"
	"github.com/juju/errors"
)

func (c *upgrade) verifyDB(ctx *cmd.Context) error {
	if len(c.args) > 0 {
		return errors.Errorf("unexpected args: %v", c.args)
	}

	models, err := getModelMachines()
	if err != nil {
		return errors.Trace(err)
	}

	server, err := getServerMachine()
	if err != nil {
		return errors.Trace(err)
	}

	ctx.Infof("Server Machine:")
	ctx.Infof("  %s, %s, %s\n\n", server.Model, server.ID, server.Address)

	ctx.Infof("Models and Machines:")
	missingAddresses := false
	for _, model := range models {
		ctx.Infof("%s (%s)", model.Name, model.UUID)
		for _, machine := range model.Machines {
			ctx.Infof("  %s: %s", machine.ID, machine.Address)
			if machine.Address == "" {
				missingAddresses = true
			}
		}
	}
	if missingAddresses {
		return errors.Errorf("there are machines with missing addresses")
	}

	db, err := NewDatabase()
	if err != nil {
		return errors.Trace(err)
	}
	defer db.Close()

	context := &dbUpgradeContext{
		cmdCtx: ctx,
		db:     db,
	}

	if err := upgradePrecheck(context); err != nil {
		return err
	}

	names, err := db.session.DatabaseNames()
	if err != nil {
		return errors.Trace(err)
	}
	for _, name := range names {
		logger.Debugf("db %q", name)
	}
	blobstore := db.session.DB("blobstore")
	names, err = blobstore.CollectionNames()
	if err != nil {
		return errors.Trace(err)
	}
	for _, name := range names {
		logger.Debugf("blobstore %q", name)
		col := blobstore.C(name)

		indices, err := col.Indexes()
		if err != nil {
			return errors.Annotatef(err, "unable to get indices for %q", name)
		}

		for _, idx := range indices {
			logger.Debugf("  index %s.%s", name, idx.Name)
		}

	}

	return nil
}
