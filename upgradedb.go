package main

import (
	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/utils"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"
)

func (c *upgrade) upgradeDB(ctx *cmd.Context) error {
	// TODO: consider other non-juju databases
	// logs, file and charm storage
	if len(c.args) > 0 {
		return errors.Errorf("unexpected args: %v", c.args)
	}
	// Try to read the agent config file.
	// assuming machine-0 of controller

	db, err := NewDatabase()
	if err != nil {
		return errors.Trace(err)
	}
	defer db.Close()

	ctx.Infof("juju db collections:")
	for _, name := range db.collections.SortedValues() {
		ctx.Infof("  %s", name)
	}

	// TODO: the first thing that should happen is to create a controller-uuid
	// This needs to be stored both in the DB and the agent config.

	controllerUUID := utils.MustNewUUID()
	ctx.Infof("Generated new controller UUID: %s", controllerUUID.String())

	// TODO: sanity check that lists all collections in the DB and verifies
	// that all collections that we don't migrate or haven't verified no-
	// change have no rows.
	if err := updateController(ctx, db, c.live); err != nil {
		return err
	}

	if err := renameAdminModel(ctx, db, c.live); err != nil {
		return err
	}
	if err := updateDefaultModel(ctx, db, c.live); err != nil {
		return err
	}

	// TODO: drop all indices
	// TODO: re-open using state.Open in order to recreate indices

	return errors.NotImplementedf("complete migration")
}

func updateController(ctx *cmd.Context, db *database, live bool) error {
	// Read 'e'
	var doc map[string]interface{}
	err := db.GetCollection("controllers").FindId("e").One(&doc)
	if err != nil {
		return errors.Trace(err)
	}

	runner := db.TransactionRunner(ctx, live)
	err = runner.RunTransaction([]txn.Op{{
		C:      "controllers",
		Id:     "e",
		Assert: txn.DocExists,
		// TODO: check mongo-space-state
		Update: bson.D{{"$set", bson.D{{"cloud", "maas"}}}},
	}})
	if err != nil {
		return errors.Trace(err)
	}

	return nil
}

func renameAdminModel(ctx *cmd.Context, db *database, live bool) error {
	// Read "admin" model

	// Rename to "controller"

	return errors.NotImplementedf("complete migration")
}

func updateDefaultModel(ctx *cmd.Context, db *database, live bool) error {
	// Read
	return errors.NotImplementedf("complete migration")
}
