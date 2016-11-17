package main

import (
	"github.com/howbazaar/b7-upgrade/b7"
	"github.com/howbazaar/b7-upgrade/rc"
	"github.com/juju/cmd"
	"github.com/juju/errors"
	jujutxn "github.com/juju/txn"
	"github.com/juju/utils/set"
	"github.com/kr/pretty"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"

	"github.com/juju/juju/environs"
	"github.com/juju/juju/mongo"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/stateenvirons"
)

type Model struct {
	Name       string
	UUID       string
	Controller bool
	Machines   []Machine
}

type Machine struct {
	ID      string
	Address string
}

type FlatMachine struct {
	Model   string
	ID      string
	Address string
}

type database struct {
	session *mgo.Session
	jujuDB  *mgo.Database
}

func NewDatabase() (_ *database, err error) {
	session, err := openSession()
	if err != nil {
		return nil, errors.Trace(err)
	}

	defer func() {
		if err != nil {
			session.Close()
		}
	}()

	jujuDB := session.DB("juju")

	db := &database{
		session: session,
		jujuDB:  jujuDB,
	}
	return db, nil
}

func (db *database) Collections() (set.Strings, error) {
	collections, err := db.jujuDB.CollectionNames()
	if err != nil {
		return nil, errors.Trace(err)
	}
	return set.NewStrings(collections...), nil
}

func (db *database) Close() {
	db.session.Close()
	db.session = nil
	db.jujuDB = nil
}

func (db *database) ControllerUUID() string {
	var docs []rc.ModelDoc
	err := db.GetCollection("models").Find(nil).All(&docs)
	if err != nil {
		panic(err)
	}
	// All docs should have the some controller id
	return docs[0].ControllerUUID
}

func (db *database) ControllerModelUUID() string {
	var controller bson.M
	err := db.GetCollection(controllersC).FindId("e").One(&controller)
	if err != nil {
		panic(err)
	}
	modelUUID := controller["model-uuid"].(string)
	return modelUUID
}

func (db *database) GetCollection(name string) *mgo.Collection {
	return db.jujuDB.C(name)
}

func (db *database) TransactionRunner(ctx *cmd.Context, live bool) jujutxn.Runner {
	params := jujutxn.RunnerParams{Database: db.jujuDB}
	runner := jujutxn.NewRunner(params)
	return &liveRunner{ctx: ctx, live: live, runner: runner}
}

type liveRunner struct {
	jujutxn.Runner

	ctx    *cmd.Context
	live   bool
	runner jujutxn.Runner
}

func (r *liveRunner) ResumeTransactions() error {
	return errors.Trace(r.runner.ResumeTransactions())
}

// Only supports the RunTransaction method, all others panic.
func (r *liveRunner) RunTransaction(ops []txn.Op) error {
	if r.live {
		err := r.runner.RunTransaction(ops)
		if err != nil {
			logger.Errorf("RunTransaction: %s\n%# v", err, pretty.Formatter(ops))
		}
		return err
	}
	logger.Debugf("RunTransaction: \n%# v", pretty.Formatter(ops))
	return nil
}

func openSession() (*mgo.Session, error) {
	config, err := getConfig()
	if err != nil {
		return nil, errors.Trace(err)
	}

	info, ok := config.MongoInfo()
	if !ok {
		return nil, errors.New("no state info available")
	}

	session, err := mongo.DialWithInfo(info.Info, mongo.DefaultDialOpts())
	if err != nil {
		return nil, errors.Annotate(err, "cannot connect to mongodb")
	}

	admin := session.DB("admin")
	if err := admin.Login(info.Tag.String(), info.Password); err != nil {
		session.Close()

		return nil, errors.Annotatef(err, "cannot log in to admin database as %q", info.Tag.String())
	}
	return session, nil
}

func openDBusingState() (*state.State, error) {
	config, err := getConfig()
	if err != nil {
		return nil, errors.Trace(err)
	}

	// NOTE: there is no controller tag in the agent config
	logger.Debugf("open state with model tag: %s", config.Model())
	logger.Debugf("expect empty controller tag: %s", config.Controller())

	info, ok := config.MongoInfo()
	if !ok {
		return nil, errors.New("no state info available")
	}

	// Open a state connection.
	return state.Open(config.Model(), config.Controller(), info, mongo.DefaultDialOpts(),
		stateenvirons.GetNewPolicyFunc(
			stateenvirons.GetNewEnvironFunc(environs.New),
		),
	)
}

func getServerMachine() (FlatMachine, error) {
	models, err := getModelMachines()
	if err != nil {
		return FlatMachine{}, errors.Trace(err)
	}

	for _, model := range models {
		for _, machine := range model.Machines {
			if model.Controller && machine.ID == "0" {
				return FlatMachine{
					Model:   model.UUID,
					ID:      machine.ID,
					Address: machine.Address,
				}, nil
			}
		}
	}
	return FlatMachine{}, errors.New("couldn't find controller machine, wat?")
}

// returns ip addresses
func getModelMachines() ([]Model, error) {

	db, err := NewDatabase()
	if err != nil {
		return nil, errors.Trace(err)
	}
	defer db.Close()

	controllerModelUUID := db.ControllerModelUUID()

	var modelDocs []b7.ModelDoc
	err = db.GetCollection("models").Find(nil).All(&modelDocs)
	if err != nil {
		return nil, errors.Trace(err)
	}

	var machineDocs []b7.MachineDoc
	err = db.GetCollection(machinesC).Find(nil).All(&machineDocs)
	if err != nil {
		return nil, errors.Trace(err)
	}

	var result []Model
	for _, model := range modelDocs {
		m := Model{
			Name:       model.Name,
			UUID:       model.UUID,
			Controller: model.UUID == controllerModelUUID,
		}
		for _, machine := range machineDocs {
			if machine.ModelUUID != model.UUID {
				continue
			}

			m.Machines = append(m.Machines, Machine{
				ID:      machine.Id,
				Address: machine.PreferredPublicAddress.Value,
			})
		}
		result = append(result, m)
	}

	return result, nil
}

func getAllMachines() ([]FlatMachine, error) {
	models, err := getModelMachines()
	if err != nil {
		return nil, errors.Trace(err)
	}

	var result []FlatMachine
	for _, model := range models {
		for _, machine := range model.Machines {
			result = append(result, FlatMachine{
				Model:   model.UUID,
				ID:      machine.ID,
				Address: machine.Address,
			})
		}
	}
	return result, nil
}

// returns ip addresses
func getOtherMachines() ([]FlatMachine, error) {
	models, err := getModelMachines()
	if err != nil {
		return nil, errors.Trace(err)
	}

	var result []FlatMachine
	for _, model := range models {
		for _, machine := range model.Machines {
			if model.Controller && machine.ID == "0" {
				continue
			}
			result = append(result, FlatMachine{
				Model:   model.UUID,
				ID:      machine.ID,
				Address: machine.Address,
			})
		}
	}
	return result, nil
}
