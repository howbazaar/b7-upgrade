package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/binarystorage"
	"github.com/juju/utils"
	"github.com/juju/utils/set"
	"github.com/juju/version"
	"github.com/kr/pretty"
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

	annotationC         = "annotations"
	applicationC        = "applications"
	cleanupsC           = "cleanups"
	cloudsC             = "clouds"
	cloudCredentialsC   = "cloudCredentials"
	cloudimagemetadataC = "cloudimagemetadata"
	constraintsC        = "constraints"
	controllersC        = "controllers"
	controllerusersC    = "controllerusers"
	endpointbindingsC   = "endpointbindings"
	ipAddressesC        = "ip.addresses"
	leasesC             = "leases"
	linklayerdevicesC   = "linklayerdevices"
	machinesC           = "machines"
	modelsC             = "models"
	modelEntityRefsC    = "modelEntityRefs"
	modelusersC         = "modelusers"
	permissionsC        = "permissions"
	providerIDsC        = "providerIDs"
	relationsC          = "relations"
	refcountsC          = "refcounts"
	resourcesC          = "resources"
	serviceC            = "services"
	sequenceC           = "sequence"
	settingsC           = "settings"
	settingsrefsC       = "settingsrefs"
	spacesC             = "spaces"
	statusesC           = "statuses"
	statusHistoryC      = "statuseshistory"
	storageconstraintsC = "storageconstraints"
	subnetsC            = "subnets"
	usermodelnameC      = "usermodelname"
	unitC               = "units"
	usersC              = "users"
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

	cloud      string
	credential string
	owner      string // admin@local
}

func (c *dbUpgradeContext) Info(args ...interface{}) {
	fmt.Fprintln(c.cmdCtx.Stdout, args...)
}

func (c *upgrade) upgradeDB(ctx *cmd.Context) error {
	// TODO: consider other non-juju databases
	// logs, file and charm storage
	if len(c.args) == 0 {
		return errors.Errorf("missing path to 2.0 tools file")
	}
	if len(c.args) > 1 {
		return errors.Errorf("unexpected args: %v", c.args[1:])
	}
	toolsFilename := c.args[0]

	// Try to read the agent config file.
	// assuming machine-0 of controller
	db, err := NewDatabase()
	if err != nil {
		return errors.Trace(err)
	}
	defer db.Close()

	// The first thing that should happen is to create a controller-uuid
	// This needs to be stored both in the DB and the agent config.
	context := &dbUpgradeContext{
		cmdCtx:         ctx,
		db:             db,
		live:           c.live,
		controllerUUID: utils.MustNewUUID().String(),
	}

	if err := upgradePrecheck(context); err != nil {
		return err
	}

	if err := cleanTxnQueue(context); err != nil {
		return err
	}

	ctx.Infof("Generated new controller UUID: %s", context.controllerUUID)

	if err := updateController(context); err != nil {
		return errors.Trace(err)
	}
	if err := updateModels(context); err != nil {
		return errors.Trace(err)
	}

	if err := renameServiceToApplication(context); err != nil {
		return errors.Trace(err)
	}

	if err := otherSchemaUpgrades(context); err != nil {
		return errors.Trace(err)
	}

	if err := updateAgentTools(context); err != nil {
		return errors.Trace(err)
	}

	// Drop all indices
	collections, err := db.Collections()
	if err != nil {
		return errors.Trace(err)
	}
	for _, name := range collections.SortedValues() {
		col := db.GetCollection(name)
		indices, err := col.Indexes()
		if err != nil {
			return errors.Annotatef(err, "unable to get indices for %q", name)
		}
		for _, idx := range indices {
			if idx.Name == "_id_" {
				continue
			}
			ctx.Infof("Drop index %s.%s", name, idx.Name)
			if c.live {
				col.DropIndexName(idx.Name)
			}
		}
	}

	if !c.live {
		ctx.Infof("skipping reopening db with state as that modifies lease clocks")
		return nil
	}

	// Re-open using state.Open in order to recreate indices
	st, err := openDBusingState()
	if err != nil {
		return errors.Annotate(err, "reopening DB using state to recreate indices")
	}
	defer st.Close()
	// Add the tools to the DB.
	if err := addTwoZeroBinariesToDB(context, st, toolsFilename); err != nil {
		return errors.Annotate(err, "adding 2.0 binaries to DB")
	}

	return nil
}

func upgradePrecheck(context *dbUpgradeContext) error {
	for _, name := range []string{
		"assignUnits",      // there should be no pending work to do
		cleanupsC,          // all cleansups should ahve been executed
		"migrations",       // migrations is beta, should have nothing in it.
		"ipaddresses",      // legacy collection, should not have data.
		"storageinstances", // structure did change, but not migrated here due to unused status.
	} {
		rows, err := context.db.GetCollection(name).Count()
		if err != nil {
			return errors.Annotatef(err, "getting row count for %q", name)
		}
		if rows > 0 {
			return errors.Errorf("%q has data, shouldn't have data", name)
		}
	}
	return nil
}

func cleanTxnQueue(context *dbUpgradeContext) error {
	if !context.live {
		return nil
	}

	context.Info("Repair controller apiHostPorts txn-queue.")
	if err := context.db.GetCollection(controllersC).UpdateId("apiHostPorts", bson.D{{"$set", bson.D{{"txn-queue", []string{}}}}}); err != nil {
		return errors.Trace(err)
	}

	context.Info("Make sure any pending transactions are complete.")
	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	if err := runner.ResumeTransactions(); err != nil {
		return errors.Trace(err)
	}

	context.Info("Clear out the txn-queue on the models.")
	if _, err := context.db.GetCollection(modelsC).UpdateAll(nil, bson.D{{"$set", bson.D{{"txn-queue", []string{}}}}}); err != nil {
		return errors.Trace(err)
	}

	return nil
}

func addTwoZeroBinariesToDB(context *dbUpgradeContext, st *state.State, toolsFilename string) error {
	storage, err := st.ToolsStorage()
	if err != nil {
		return errors.Annotate(err, "getting tools storage")
	}
	defer storage.Close()

	tools, err := os.Open(toolsFilename)
	if err != nil {
		return errors.Annotatef(err, "problem opening %q", toolsFilename)
	}
	defer tools.Close()

	// Read the tools tarball from the request, calculating the sha256 along the way.
	data, sha256, err := readAndHash(tools)
	if err != nil {
		return err
	}

	if context.live {
		blobstore := context.db.session.DB("blobstore")
		err := blobstore.C("blobstore.chunks").DropIndexName("files_id_1_n_1")
		if err != nil {
			return errors.Trace(err)
		}
	} else {
		context.Info("removing index from blobstore")
	}

	for _, v := range []version.Binary{
		// Almost 100% sure we don't need trusty tools.
		// version.MustParseBinary("2.0.0-trusty-amd64"),
		version.MustParseBinary("2.0.0-xenial-amd64"),
	} {
		metadata := binarystorage.Metadata{
			Version: v.String(),
			Size:    int64(len(data)),
			SHA256:  sha256,
		}
		logger.Debugf("uploading tools %+v to storage", metadata)
		if context.live {
			if err := storage.Add(bytes.NewReader(data), metadata); err != nil {
				return errors.Trace(err)
			}
		}
	}
	return nil
}

func readAndHash(r io.Reader) (data []byte, sha256hex string, err error) {
	hash := sha256.New()
	data, err = ioutil.ReadAll(io.TeeReader(r, hash))
	if err != nil {
		return nil, "", errors.Annotate(err, "error processing file upload")
	}
	return data, fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func updateController(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Adding controller settings")
	controllers := context.db.GetCollection(controllersC)
	var doc bson.M
	err := controllers.FindId("stateServingInfo").One(&doc)
	if err != nil {
		return errors.Annotate(err, "getting stateServingInfo")
	}

	modelUUID := context.db.ControllerModelUUID()
	logger.Debugf("controller model-uuid: %s", modelUUID)

	var controllerModel b7.ModelDoc
	err = context.db.GetCollection(modelsC).FindId(modelUUID).One(&controllerModel)
	if err != nil {
		return errors.Annotate(err, "getting controller model")
	}

	var controllerSettings b7.SettingsDoc
	err = context.db.GetCollection(settingsC).FindId(modelUUID + ":e").One(&controllerSettings)
	if err != nil {
		return errors.Annotate(err, "getting controller model settings")
	}

	logger.Debugf("controllerSettings: %# v", pretty.Formatter(controllerSettings))

	// Also add in doc for controller settings.
	settings := map[string]interface{}{
		"api-port":                doc["apiport"],
		"auditing-enabled":        false,
		"ca-cert":                 controllerSettings.Settings["ca-cert"],
		"controller-uuid":         context.controllerUUID,
		"set-numa-control-policy": false,
		"state-port":              doc["stateport"],
	}

	context.cloud = controllerSettings.Settings["type"].(string)
	context.owner = controllerModel.Owner
	credentialID := fmt.Sprintf("%s#%s#%s", context.cloud, context.owner, context.cloud)
	context.credential = fmt.Sprintf("%s/%s/%s", context.cloud, context.owner, context.cloud)
	cloud := rc.CloudDoc{
		Name: context.cloud,
		Type: context.cloud,
	}
	credentails := rc.CloudCredentialDoc{
		Owner: context.owner,
		Cloud: context.cloud,
		Name:  context.cloud,
	}
	switch context.cloud {
	case "lxd":
		cloud.AuthTypes = []string{"empty"}
		cloud.Regions = map[string]rc.CloudRegionSubdoc{
			"localhost": rc.CloudRegionSubdoc{},
		}
		credentails.AuthType = "empty"
		if err := writeLXDCerts(controllerSettings, context.live); err != nil {
			return errors.Annotate(err, "failed to write out lxd certs")
		}
	case "maas":
		cloud.AuthTypes = []string{"oauth1"}
		cloud.Endpoint = controllerSettings.Settings["maas-server"].(string)
		credentails.AuthType = "oauth1"
		credentails.Attributes = map[string]string{
			"maas-oauth": controllerSettings.Settings["maas-oauth"].(string),
		}
	default:
		return errors.Errorf("unsupported type %q", context.cloud)
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	err = runner.RunTransaction([]txn.Op{{
		C:      controllersC,
		Id:     "e",
		Assert: txn.DocExists,
		Update: bson.D{{"$set", bson.D{{"cloud", context.cloud}}}},
	}, {
		C:      cloudsC,
		Id:     context.cloud,
		Assert: txn.DocMissing,
		Insert: cloud,
	}, {
		C:      cloudCredentialsC,
		Id:     credentialID,
		Assert: txn.DocMissing,
		Insert: credentails,
	}, createSettingsOp("controllers", "controllerSettings", settings), {
		C:      permissionsC,
		Id:     fmt.Sprintf("c#%s#us#admin@local", context.controllerUUID),
		Assert: txn.DocMissing,
		Insert: bson.M{
			"access":             "superuser",
			"object-global-key":  "c#" + context.controllerUUID,
			"subject-global-key": "us#admin@local",
		},
	}, {
		C:      controllerusersC,
		Id:     "admin@local",
		Assert: txn.DocMissing,
		Insert: bson.M{
			"createdby":   "admin@local",
			"datecreated": time.Date(2016, 11, 18, 12, 0, 0, 0, time.UTC),
			"displayname": "admin",
			"object-uuid": context.controllerUUID,
			"user":        "admin@local",
		},
	}})
	if err != nil {
		return errors.Trace(err)
	}

	context.controllerSettings = settings
	return nil
}

func writeLXDCerts(controllerSettings b7.SettingsDoc, live bool) error {
	if live {
		if err := os.MkdirAll("/etc/juju", 0755); err != nil {
			return errors.Trace(err)
		}
	} else {
		logger.Debugf("mkdir /etc/juju")
	}
	for _, action := range []struct {
		setting  string
		filename string
	}{
		{"client-cert", "lxd-client.crt"},
		{"client-key", "lxd-client.key"},
		{"server-cert", "lxd-server.crt"},
	} {
		content := controllerSettings.Settings[action.setting].(string)
		filename := "/etc/juju/" + action.filename
		if live {
			err := ioutil.WriteFile(filename, []byte(content), 0600)
			if err != nil {
				return errors.Trace(err)
			}
		} else {
			logger.Debugf("writing %s:\n%s\n------", filename, content)
		}
	}
	return nil
}

func removeSettingsBSOND(values set.Strings) bson.D {
	var result bson.D
	for _, name := range values.SortedValues() {
		result = append(result, bson.DocElem{"settings." + name, nil})
	}
	return result
}

func updateModels(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Updating models")
	coll := context.db.GetCollection("models")

	var ops []txn.Op
	var doc b7.ModelDoc
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		removed := set.NewStrings(
			"admin-secret",
			"api-port",
			"bootstrap-addresses-delay",
			"bootstrap-retry-delay",
			"bootstrap-timeout",
			"ca-cert",
			"ca-private-key",
			"controller-uuid",
			"lxc-clone-aufs",
			"prefer-ipv6",
			"set-numa-control-policy",
			"state-port",
			"tools-metadata-url",
		)

		switch context.cloud {
		case "lxd":
			removed.Union(set.NewStrings(
				"client-cert",
				"client-key",
				"namespace",
				"remote-url",
				"server-cert",
			))
		case "maas":
			removed.Union(set.NewStrings(
				"maas-server",
				"maas-oauth",
				"maas-agent-name",
			))
		default:
			return errors.Errorf("unsupported cloud %q", context.cloud)
		}

		settingsKey := doc.UUID + ":e"

		updates := bson.D{
			{"cloud", context.cloud},
			{"cloud-credential", context.credential},
			{"controller-uuid", context.controllerUUID},
		}
		if doc.Name == "admin" {
			updates = append(updates, bson.DocElem{"name", "controller"})
		}
		ops = append(ops, txn.Op{
			C:      modelsC,
			Id:     doc.UUID,
			Assert: txn.DocExists,
			Update: bson.D{
				{"$set", updates},
				{"$unset", bson.D{{"server-uuid", nil}}},
			},
		}, txn.Op{
			C:      permissionsC,
			Id:     fmt.Sprintf("e#%s#us#admin@local", doc.UUID),
			Assert: txn.DocMissing,
			Insert: bson.M{
				"access":             "admin",
				"object-global-key":  "e#" + doc.UUID,
				"subject-global-key": "us#admin@local",
			},
		}, txn.Op{
			C:      settingsC,
			Id:     settingsKey,
			Assert: txn.DocExists,
			Update: bson.D{
				{"$set", bson.D{
					{"settings.agent-version", "2.0.0"},
					{"settings.provisioner-harvest-mode", "destroyed"},
					{"settings.transmit-vendor-metrics", true},
				}},
				{"$unset", removeSettingsBSOND(removed)},
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
	context.Info("Renaming services to applications")
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
	if err := updateCollectionIDGlobalKey(context, statusesC, ""); err != nil {
		return errors.Trace(err)
	}
	if err := updateStatusHistoryCollection(context); err != nil {
		return errors.Trace(err)
	}
	// Remove old settingsrefs collection.
	if context.live {
		if err := context.db.GetCollection(serviceC).DropCollection(); err != nil {
			return errors.Annotate(err, "drop services")
		}
		if err := context.db.GetCollection(settingsrefsC).DropCollection(); err != nil {
			return errors.Annotate(err, "drop settings refs")
		}
		if err := context.db.GetCollection("ipaddresses").DropCollection(); err != nil {
			return errors.Annotate(err, "drop ipaddresses")
		}
	} else {
		context.Info("Drop", serviceC, "collection.")
		context.Info("Drop", settingsrefsC, "collection.")
		context.Info("Drop", "ipaddresses", "collection.")
	}
	return nil
}

func otherSchemaUpgrades(context *dbUpgradeContext) error {
	context.Info("Other DB upgrades")
	if err := upgradeUsersCollection(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeModelUsersCollection(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeMachinesCollection(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeCloudImageMetadata(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeSpaces(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeSubnets(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeLinkLayerDevices(context); err != nil {
		return errors.Trace(err)
	}
	if err := upgradeIPAddresses(context); err != nil {
		return errors.Trace(err)
	}

	return nil
}

func moveDocsFromServicesToApplications(context *dbUpgradeContext) error {
	fmt.Fprintln(context.cmdCtx.Stdout, "Moving documents from services collection to applications")
	coll := context.db.GetCollection(serviceC)
	storageconstraints := context.db.GetCollection(storageconstraintsC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	refCounts := make(map[string]int)
	for iter.Next(&doc) {
		id := getStringField(doc, "_id")
		data := copyBsonDField(doc)
		data = removeBsonDField(data, "ownertag")

		modelUUID, appName, ok := splitDocID(id)
		if !ok {
			return errors.Errorf("failed to split id %q", id)
		}
		charmurl := getStringField(data, "charmurl")
		unitcount := getIntField(data, "unitcount")
		// Remove old storage constrant doc, and add new one
		oldConstraintID := modelUUID + ":s#" + appName
		newConstraintID := modelUUID + ":asc#" + appName + "#" + charmurl
		appGlobalKey := modelUUID + ":a#" + appName + "#" + charmurl
		charmGlobalKey := modelUUID + ":c#" + charmurl
		for _, key := range []string{charmGlobalKey, appGlobalKey, newConstraintID} {
			val := refCounts[key]
			refCounts[key] = val + 1 + unitcount
		}

		var constraintsDoc bson.D
		err := storageconstraints.FindId(oldConstraintID).One(&constraintsDoc)
		if err != nil {
			return errors.Annotatef(err, "couldn't find storage constraints for %q", oldConstraintID)
		}
		removeBsonDField(constraintsDoc, "_id")

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
			txn.Op{
				C:      storageconstraintsC,
				Id:     oldConstraintID,
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      storageconstraintsC,
				Id:     newConstraintID,
				Assert: txn.DocMissing,
				Insert: constraintsDoc,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "failed to read services")
	}

	for key, value := range refCounts {
		uuid, _, _ := splitDocID(key)
		ops = append(ops, txn.Op{
			C:      refcountsC,
			Id:     key,
			Assert: txn.DocMissing,
			Insert: bson.D{
				{"model-uuid", uuid},
				{"refcount", value},
			},
		})
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
		namespace, ok := readBsonDField(doc, "namespace")
		if namespace == "application-leadership" {
			return errors.Errorf("lease doc already has application-leadership: %q", oldID)
		}
		if !ok || namespace != "service-leadership" {
			logger.Debugf("skipping lease with id %q", oldID)
			continue
		}

		data := copyBsonDField(doc)
		replaceBsonDField(data, "namespace", "application-leadership")

		newID := getStringField(data, "model-uuid") + ":" + getStringField(data, "type") + "#application-leadership#"
		if name := getStringField(data, "name"); name != "" {
			newID += name + "#"
		}

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
			epData, ok := ep.(bson.M)
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

func upgradeUsersCollection(context *dbUpgradeContext) error {
	context.Info("Updating users")
	coll := context.db.GetCollection(usersC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		ops = append(
			ops,
			txn.Op{
				C:      usersC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$unset", bson.D{{"deactivated", nil}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read users")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeModelUsersCollection(context *dbUpgradeContext) error {
	context.Info("Updating model users")
	coll := context.db.GetCollection(modelusersC)

	var ops []txn.Op

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		ops = append(
			ops,
			txn.Op{
				C:      modelusersC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", bson.D{{"object-uuid", doc["model-uuid"]}}},
					{"$unset", bson.D{{"access", nil}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read model users")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeMachinesCollection(context *dbUpgradeContext) error {
	context.Info("Updating machines")
	coll := context.db.GetCollection(machinesC)

	var ops []txn.Op

	var doc b7.MachineDoc
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		var containers []string
		for _, value := range doc.SupportedContainers {
			if value != "lxc" {
				containers = append(containers, value)
			}
		}

		ops = append(
			ops,
			txn.Op{
				C:      machinesC,
				Id:     doc.DocID,
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", bson.D{{"supportedcontainers", containers}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read machines")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeSpaces(context *dbUpgradeContext) error {
	context.Info("Updating spaces")
	coll := context.db.GetCollection(spacesC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		modelUUID := getStringField(doc, "model-uuid")
		providerID := getStringField(doc, "providerid")

		if strings.HasPrefix(providerID, modelUUID) {
			providerID = providerID[len(modelUUID)+1:]
			ops = append(
				ops,
				txn.Op{
					C:      spacesC,
					Id:     getStringField(doc, "_id"),
					Assert: txn.DocExists,
					Update: bson.D{
						{"$set", bson.D{{"providerid", providerID}}},
					},
				},
				txn.Op{
					C:      providerIDsC,
					Id:     fmt.Sprintf("%s:space:%s", modelUUID, providerID),
					Assert: txn.DocMissing,
					Insert: bson.M{
						"model-uuid": modelUUID,
					},
				},
			)
		}
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read spaces")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeSubnets(context *dbUpgradeContext) error {
	context.Info("Updating subnets")
	coll := context.db.GetCollection(subnetsC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		modelUUID := getStringField(doc, "model-uuid")
		providerID := getStringField(doc, "providerid")

		if strings.HasPrefix(providerID, modelUUID) {
			providerID = providerID[len(modelUUID)+1:]
			ops = append(
				ops,
				txn.Op{
					C:      subnetsC,
					Id:     getStringField(doc, "_id"),
					Assert: txn.DocExists,
					Update: bson.D{
						{"$set", bson.D{{"providerid", providerID}}},
					},
				},
				txn.Op{
					C:      providerIDsC,
					Id:     fmt.Sprintf("%s:subnet:%s", modelUUID, providerID),
					Assert: txn.DocMissing,
					Insert: bson.M{
						"model-uuid": modelUUID,
					},
				},
			)
		}
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read subnets")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeLinkLayerDevices(context *dbUpgradeContext) error {
	context.Info("Updating link layer devices")
	coll := context.db.GetCollection(linklayerdevicesC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		modelUUID := getStringField(doc, "model-uuid")
		providerID := getStringField(doc, "providerid")

		if strings.HasPrefix(providerID, modelUUID) {
			providerID = providerID[len(modelUUID)+1:]
			ops = append(
				ops,
				txn.Op{
					C:      linklayerdevicesC,
					Id:     getStringField(doc, "_id"),
					Assert: txn.DocExists,
					Update: bson.D{
						{"$set", bson.D{{"providerid", providerID}}},
					},
				},
				txn.Op{
					C:      providerIDsC,
					Id:     fmt.Sprintf("%s:linklayerdevice:%s", modelUUID, providerID),
					Assert: txn.DocMissing,
					Insert: bson.M{
						"model-uuid": modelUUID,
					},
				},
			)
		}
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to link layer devices")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeIPAddresses(context *dbUpgradeContext) error {
	context.Info("Updating ip.addresses")
	coll := context.db.GetCollection(ipAddressesC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		modelUUID := getStringField(doc, "model-uuid")
		providerID := getStringField(doc, "providerid")

		if strings.HasPrefix(providerID, modelUUID) {
			providerID = providerID[len(modelUUID)+1:]
			ops = append(
				ops,
				txn.Op{
					C:      ipAddressesC,
					Id:     getStringField(doc, "_id"),
					Assert: txn.DocExists,
					Update: bson.D{
						{"$set", bson.D{{"providerid", providerID}}},
					},
				},
				txn.Op{
					C:      providerIDsC,
					Id:     fmt.Sprintf("%s:address:%s", modelUUID, providerID),
					Assert: txn.DocMissing,
					Insert: bson.M{
						"model-uuid": modelUUID,
					},
				},
			)
		}
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read ip.addresses")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
}

func upgradeCloudImageMetadata(context *dbUpgradeContext) error {
	context.Info("Updating cloudimagemetadata")
	coll := context.db.GetCollection(cloudimagemetadataC)

	var ops []txn.Op

	var doc bson.D
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {
		oldID := getStringField(doc, "_id")
		_, localID, ok := splitDocID(oldID)
		if !ok {
			continue
		}

		data := copyBsonDField(doc)
		removeBsonDField(data, "model-uuid")

		ops = append(
			ops,
			txn.Op{
				C:      cloudimagemetadataC,
				Id:     oldID,
				Assert: txn.DocExists,
				Remove: true,
			},
			txn.Op{
				C:      cloudimagemetadataC,
				Id:     localID,
				Assert: txn.DocMissing,
				Insert: data,
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read cloutimagemetadata")
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

func updateStatusHistoryCollection(context *dbUpgradeContext) error {
	context.Info("Updating status history")
	coll := context.db.GetCollection(statusHistoryC)

	var doc bson.M
	iter := coll.Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		key := doc["globalkey"].(string)
		if !strings.HasPrefix(key, "s#") {
			continue
		}

		newKey := "a" + key[1:]

		if context.live {
			if err := coll.UpdateId(doc["_id"], bson.D{{"$set", bson.D{{"globalkey", newKey}}}}); err != nil {
				return errors.Trace(err)
			}
		} else {
			logger.Debugf("update %q, set globalkey to %q", doc["_id"].(bson.ObjectId), newKey)
		}
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to status history")
	}
	return nil
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
	context.Info("Updating tools field on units and machines")
	var ops []txn.Op

	// This code assumes a homogeneous environment of xenial amd64 machines.
	const agentVersion = "2.0.0-xenial-amd64"

	var doc bson.M
	iter := context.db.GetCollection(machinesC).Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		ops = append(
			ops,
			txn.Op{
				C:      machinesC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", bson.D{{"tools.version", agentVersion}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read machines for version update")
	}

	iter = context.db.GetCollection(unitC).Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&doc) {

		ops = append(
			ops,
			txn.Op{
				C:      unitC,
				Id:     doc["_id"],
				Assert: txn.DocExists,
				Update: bson.D{
					{"$set", bson.D{{"tools.version", agentVersion}}},
				},
			},
		)
	}
	if err := iter.Err(); err != nil {
		return errors.Annotatef(err, "failed to read units for version update")
	}

	runner := context.db.TransactionRunner(context.cmdCtx, context.live)
	return errors.Trace(runner.RunTransaction(ops))
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
	// remove three fields
	for _, field := range []string{"_id", "txn-revno", "txn-queue"} {
		result = removeBsonDField(result, field)
	}
	return result
}

// getStringField returns empty string if name not found or name not a string
func getStringField(d bson.D, name string) string {
	v, _ := readBsonDField(d, name)
	s, _ := v.(string)
	return s
}

// getIntField returns 0 if name not found or name not an int.
func getIntField(d bson.D, name string) int {
	v, _ := readBsonDField(d, name)
	i, _ := v.(int)
	return i
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
