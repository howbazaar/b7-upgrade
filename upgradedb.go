package main

import (
	"fmt"
	"strings"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/utils"
	"gopkg.in/juju/names.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"

	"github.com/howbazaar/b7-upgrade/b7"
	"github.com/howbazaar/b7-upgrade/rc"
)

// See: http://docs.mongodb.org/manual/faq/developers/#faq-dollar-sign-escaping
// for why we're using those replacements.
const (
	fullWidthDot    = "\uff0e"
	fullWidthDollar = "\uff04"

	annotationC    = "annotations"
	applicationC   = "applications"
	serviceC       = "services"
	sequenceC      = "sequence"
	usermodelnameC = "usermodelname"
	unitC          = "units"
)

var (
	escapeReplacer   = strings.NewReplacer(".", fullWidthDot, "$", fullWidthDollar)
	unescapeReplacer = strings.NewReplacer(fullWidthDot, ".", fullWidthDollar, "$")
)

type dbUpgradeContext struct {
	cmdCtx             *cmd.Context
	db                 *database
	live               bool
	controllerUUID     string
	controllerSettings map[string]interface{}
}

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
	context := &dbUpgradeContext{
		cmdCtx:         ctx,
		db:             db,
		live:           c.live,
		controllerUUID: utils.MustNewUUID().String(),
	}

	ctx.Infof("Generated new controller UUID: %s", context.controllerUUID)

	// TODO: sanity check that lists all collections in the DB and verifies
	// that all collections that we don't migrate or haven't verified no-
	// change have no rows.
	if err := updateController(context); err != nil {
		return err
	}
	if err := updateModels(context); err != nil {
		return err
	}

	if err := renameServiceToApplication(context); err != nil {
		return err
	}

	if err := updateAgentTools(context); err != nil {
		// "tools" in unit and machine.
		return err
	}

	// TODO: drop all indices
	// TODO: re-open using state.Open in order to recreate indices

	return errors.NotImplementedf("complete migration")
}

func updateController(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Adding controller settings")
	var doc map[string]interface{}
	err := context.db.GetCollection("controllers").FindId("stateServingInfo").One(&doc)
	if err != nil {
		return errors.Trace(err)
	}

	// Also add in doc for controller settings.
	settings := map[string]interface{}{
		"api-port":                doc["apiport"],
		"auditing-enabled":        false,
		"ca-cert":                 doc["cert"],
		"controller-uuid":         context.controllerUUID,
		"set-numa-control-policy": false,
		"state-port":              doc["stateport"],
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	err = runner.RunTransaction([]txn.Op{{
		C:      "controllers",
		Id:     "e",
		Assert: txn.DocExists,
		Update: bson.D{{"$set", bson.D{{"cloud", "maas"}}}},
	}, createSettingsOp("controllers", "controllerSettings", settings),
	})
	if err != nil {
		return errors.Trace(err)
	}

	context.controllerSettings = settings
	return nil
}

func updateModels(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating models")
	coll := context.db.GetCollection("models")

	var ops []txn.Op

	var doc map[string]interface{}
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		updates := bson.D{
			{"cloud", "maas"},
			{"cloud-credential", "maas/admin@local/maas"}, // TODO: check this credential key
			{"controller-uuid", context.controllerUUID},
		}
		if doc["name"] == "admin" {
			updates = append(updates, bson.DocElem{"name", "controller"})
		}
		ops = append(ops, txn.Op{
			C:      "models",
			Id:     doc["_id"],
			Assert: txn.DocExists,
			Update: bson.D{
				{"$set", updates},
				{"$unset", bson.D{{"server-uuid", nil}}},
			},
		})
	}
	ops = append(ops, txn.Op{
		C:      usermodelnameC,
		Id:     "admin@local:admin",
		Assert: txn.DocExists,
		Remove: true,
	}, txn.Op{
		C:      usermodelnameC,
		Id:     "admin@local:controller",
		Assert: txn.DocMissing,
		Insert: bson.M{},
	})

	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "failed to read models")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func renameServiceToApplication(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Renaming services to applications")
	if err := moveDocsFromServicesToApplications(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateUnits(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateAnnotations(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateSequences(context); err != nil {
		return errors.Trace(err)
	}
	// settings store global key

}

func moveDocsFromServicesToApplications(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Moving documents from services collection to applications")
	serviceCollection := context.db.GetCollection(serviceC)

	var ops []txn.Op

	var doc map[string]interface{}
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		data := copyMap(doc, nil)
		delete(data, "ownertag")
		ops = append(
			ops,
			txn.Op{
				C:      serviceC,
				Id:     data["_id"],
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      applicationC,
				Id:     data["_id"],
				Assert: txn.DocMissing,
				Insert: data,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "failed to read services")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateUnits(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating units")
	units := context.db.GetCollection(unitC)

	var ops []txn.Op

	var doc map[string]interface{}
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		ops = append(ops, txn.Op{
			C:      units,
			Id:     doc["_id"],
			Assert: txn.DocExists,
			Update: bson.D{
				{"$set", bson.D{{"application", doc["service"]}}},
				{"$unset", bson.D{
					{"ports", nil},
					{"privateaddress", nil},
					{"publicaddress", nil},
					{"service", nil},
				}},
			},
		})
	}
	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "failed to read units")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateAnnotations(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating annotations")
	annotations := context.db.GetCollection(annotationC)

	var ops []txn.Op

	var doc map[string]interface{}
	iter := annotations.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		key := doc["globalkey"]
		if !strings.HasPrefix(key, "s#") {
			fmt.Fprintf(context.cmdCtx.Stdout, "Skipping %s\n", key)
			continue
		}
		data := copyMap(doc, nil)
		newKey := "a" + key[1:]
		newID := data["model-uuid"] + ":" + newKey
		data["_id"] = newID // this isn't actually necessary, but will make the output look consistent
		data["globalkey"] = newKey

		ops = append(
			ops,
			txn.Op{
				C:      annotationC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      annotationC,
				Id:     newID,
				Assert: txn.DocMissing,
				Insert: data,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "failed to read annotations")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateSequence(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating sequences")
	sequences := context.db.GetCollection(sequenceC)

	var ops []txn.Op

	var doc map[string]interface{}
	iter := sequences.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		serviceTag, err := b7.ParseServiceTag(doc["name"])
		if err != nil {
			// not a service serquence
			continue
		}

		appTag := names.NewApplicationTag(serviceTag.Id())

		data := copyMap(doc, nil)
		newID := data["model-uuid"] + ":" + appTag.String()
		data["_id"] = newID // this isn't actually necessary, but will make the output look consistent
		data["name"] = appTag.String()

		ops = append(
			ops,
			txn.Op{
				C:      sequenceC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      sequenceC,
				Id:     newID,
				Assert: txn.DocMissing,
				Insert: data,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "failed to read sequences")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateAgentTools(context *dbUpgradeContext) error {
	// obviously we need to insert all docs from service -> application collection

	// annotations store global key, so service -> app
	// settings store global key
	return errors.NotImplementedf("updateAgentTools")
}

func createSettingsOp(collection, key string, values map[string]interface{}) txn.Op {
	newValues := copyMap(values, escapeReplacer.Replace)
	return txn.Op{
		C:      collection,
		Id:     key,
		Assert: txn.DocMissing,
		Insert: &rc.SettingsDoc{
			Settings: newValues,
		},
	}
}

// copyMap copies the keys and values of one map into a new one.  If replace
// is non-nil, for each old key k, the new key will be replace(k).
func copyMap(in map[string]interface{}, replace func(string) string) (out map[string]interface{}) {
	out = make(map[string]interface{})
	for key, value := range in {
		if replace != nil {
			key = replace(key)
		}
		out[key] = value
	}
	return
}

// splitDocID returns the 2 parts of model UUID prefixed
// document ID. If the id is not in the expected format the final
// return value will be false.
func splitDocID(id string) (string, string, bool) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func localID(ID string) string {
	modelUUID, localID, ok := splitDocID(ID)
	if !ok {
		return ID
	}
	return localID
}
