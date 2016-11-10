package main

import (
	"fmt"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"
)

func (c *upgrade) cleanDB(ctx *cmd.Context) error {
	// Try to read the agent config file.
	// assuming machine-0 of controller
	db, err := NewDatabase()
	if err != nil {
		return errors.Trace(err)
	}
	defer db.Close()

	fmt.Fprintln(ctx.Stdout, "Cleaning cleanups")

	return c.removeCleanups(ctx, db)
}

func (c *upgrade) removeCleanups(ctx *cmd.Context, db *database) error {
	coll := db.GetCollection(cleanupsC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		fmt.Fprintf(ctx.Stdout, "Adding %s: %q\n", doc["kind"], doc["prefix"])

		ops = append(ops, txn.Op{
			C:      cleanupsC,
			Id:     doc["_id"],
			Remove: true,
		})
	}
	if err := iter.Err(); err != nil {
		return errors.Trace(err)
	}

	if len(ops) > 0 {
		runner := db.TransactionRunner(ctx, c.live)
		if err := runner.RunTransaction(ops); err != nil {
			return errors.Trace(err)
		}
	}

	if c.live {
		fmt.Fprintf(ctx.Stdout, "%d cleanup docs removed.\n", len(ops))
	} else {
		fmt.Fprintf(ctx.Stdout, "%d cleanup docs would be removed.\n", len(ops))
	}
	return nil
}
