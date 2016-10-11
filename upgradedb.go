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

	annotationC       = "annotations"
	applicationC      = "applications"
	constraintsC      = "constraints"
	endpointbindingsC = "endpointbindings"
	leasesC           = "leases"
	modelEntityRefsC  = "modelEntityRefs"
	relationsC        = "relations"
	resourcesC        = "resources"
	serviceC          = "services"
	sequenceC         = "sequence"
	settingsC         = "settings"
	usermodelnameC    = "usermodelname"
	unitC             = "units"
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

func (c *dbUpgradeContext) Info(args ...interface{}) {
	fmt.Fprintln(c.cmdCtx.Stdout, args...)
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
		// add tools to db

		// "tools" in unit and machine.
		return err
	}

	// TODO: drop all indices
	// TODO: re-open using state.Open in order to recreate indices

	return errors.NotImplementedf("complete migration")
}

func updateController(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Adding controller settings")
	var doc bson.M
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

	var doc bson.M
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
	if err := updateLeases(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateModelEntityRefs(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateRelations(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateResources(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateCollectionIDGlobalKey(context, annotationC, "globalkey"); err != nil {
		return errors.Trace(err)
	}
	if err := updateCollectionIDGlobalKey(context, constraintsC, ""); err != nil {
		return errors.Trace(err)
	}
	if err := updateCollectionIDGlobalKey(context, endpointbindingsC, ""); err != nil {
		return errors.Trace(err)
	}
	if err := updateCollectionIDGlobalKey(context, settingsC, ""); err != nil {
		return errors.Trace(err)
	}
	return nil
}

func moveDocsFromServicesToApplications(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Moving documents from services collection to applications")
	coll := context.db.GetCollection(serviceC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		data := copyBsonDField(doc)
		data = removeBsonDField(data, "ownertag")
		id := getStringField(data, "_id")
		ops = append(
			ops,
			txn.Op{
				C:      serviceC,
				Id:     id,
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      applicationC,
				Id:     id,
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
	coll := context.db.GetCollection(unitC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		ops = append(ops, txn.Op{
			C:      unitC,
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

func updateLeases(context *dbUpgradeContext) error {
	context.Info("Updating leases")
	coll := context.db.GetCollection(leasesC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		oldID := getStringField(doc, "_id")
		if namespace, ok := readBsonDField(doc, "namespace"); !ok || namespace != "service-leadership" {
			continue
		}

		data := copyBsonDField(doc)
		replaceBsonDField(data, "namespace", "application-leadership")

		newID := getStringField(data, "model-uuid") + ":" + getStringField(data, "type") + "#application-leadership#"
		if name := getStringField(data, "name"); name != "" {
			newID += name + "#"
		}
		// this isn't actually necessary, but will make the output look consistent
		replaceBsonDField(data, "_id", newID)

		ops = append(
			ops,
			txn.Op{
				C:      leasesC,
				Id:     oldID,
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      leasesC,
				Id:     newID,
				Assert: txn.DocMissing,
				Insert: data,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read leases")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateModelEntityRefs(context *dbUpgradeContext) error {
	context.Info("Updating modelEntityRefs")
	coll := context.db.GetCollection(modelEntityRefsC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		apps := doc["services"]
		ops = append(
			ops,
			txn.Op{
				C:      modelEntityRefsC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", bson.D{{"applications", apps}}},
					{"$unset", bson.D{{"services", nil}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read modelEntityRefs")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateRelations(context *dbUpgradeContext) error {
	context.Info("Updating relations")
	coll := context.db.GetCollection(relationsC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		var setFields bson.D
		var unsetFields bson.D
		endpoints, ok := doc["endpoints"].([]interface{})
		if !ok {
			return errors.Errorf("programming error reading relations endpoints, type %T", doc["endpoints"])
		}
		for i, ep := range endpoints {
			epData, ok := ep.(map[string]interface{})
			if !ok {
				return errors.Errorf("programming error iterating relations endpoints, ep type %T", ep)
			}
			prefix := fmt.Sprintf("endpoints.%d.", i)
			setFields = append(setFields, bson.DocElem{prefix + "applicationname", epData["servicename"]})
			unsetFields = append(unsetFields, bson.DocElem{prefix + "servicename", nil})
		}
		ops = append(
			ops,
			txn.Op{
				C:      relationsC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", setFields},
					{"$unset", unsetFields},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read relations")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateResources(context *dbUpgradeContext) error {
	context.Info("Updating resources")
	coll := context.db.GetCollection(resourcesC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		appID := doc["service-id"]

		ops = append(
			ops,
			txn.Op{
				C:      resourcesC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", bson.D{{"application-id", appID}}},
					{"$unset", bson.D{{"service-id", nil}, {"env-uuid", nil}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read resources")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateCollectionIDGlobalKey(context *dbUpgradeContext, collection string, keyName string) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating", collection)
	coll := context.db.GetCollection(collection)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		oldID := getStringField(doc, "_id")
		model, localID, ok := splitDocID(oldID)
		if !ok {
			continue
		}
		if !strings.HasPrefix(localID, "s#") {
			continue
		}
		data := copyBsonDField(doc)
		newKey := "a" + localID[1:]
		newID := model + ":" + newKey
		// this isn't actually necessary, but will make the output look consistent
		replaceBsonDField(data, "_id", newID)
		if keyName != "" {
			replaceBsonDField(data, keyName, newKey)
		}
		// we need to delete "env-uuid" from endpoint bindings, this function
		// is used for multiple collections, but none of them define env-uuid, so we should
		// be fine to just call delete on the map.
		removeBsonDField(data, "env-uuid")

		ops = append(
			ops,
			txn.Op{
				C:      collection,
				Id:     oldID,
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      collection,
				Id:     newID,
				Assert: txn.DocMissing,
				Insert: data,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read %s", collection)
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func updateSequence(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating sequences")
	sequences := context.db.GetCollection(sequenceC)

	var ops []txn.Op

	var doc bson.D
	iter := sequences.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		serviceTag, err := b7.ParseServiceTag(getStringField(doc, "name"))
		if err != nil {
			// not a service serquence
			continue
		}

		appTag := names.NewApplicationTag(serviceTag.Id())

		data := copyBsonDField(doc)
		newID := getStringField(data, "model-uuid") + ":" + appTag.String()
		// this isn't actually necessary, but will make the output look consistent
		replaceBsonDField(data, "_id", newID)
		replaceBsonDField(data, "name", appTag.String())

		ops = append(
			ops,
			txn.Op{
				C:      sequenceC,
				Id:     getStringField(doc, "_id"),
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
	_, localID, ok := splitDocID(ID)
	if !ok {
		return ID
	}
	return localID
}

func copyBsonDField(src bson.D) bson.D {
	result := make(bson.D, len(src))
	copy(result, src)
	return result
}

// getStringField returns empty string if name not found or name not a string
func getStringField(d bson.D, name string) string {
	v, _ := readBsonDField(d, name)
	s, _ := v.(string)
	return s
}

// readBsonDField returns the value of a given field in a bson.D.
func readBsonDField(d bson.D, name string) (interface{}, bool) {
	for _, field := range d {
		if field.Name == name {
			return field.Value, true
		}
	}
	return nil, false
}

func removeBsonDField(d bson.D, name string) bson.D {
	for i, field := range d {
		if field.Name == name {
			r := append(bson.D{}, d[0:i]...)
			return append(r, d[i+1:]...)
		}
	}
	return d
}

func renameBsonDField(d bson.D, fromName, toName string) {
	for i, field := range d {
		if field.Name == fromName {
			d[i].Name = toName
			return
		}
	}
}

// replaceBsonDField replaces a field in bson.D.
func replaceBsonDField(d bson.D, name string, value interface{}) {
	for i, field := range d {
		if field.Name == name {
			d[i].Value = value
			return
		}
	}
}
