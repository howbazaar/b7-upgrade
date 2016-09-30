package main

import (
	"github.com/juju/cmd"
	"github.com/juju/errors"
	jujutxn "github.com/juju/txn"
	"github.com/juju/utils/set"
	"gopkg.in/juju/names.v2"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/txn"

	"github.com/juju/juju/environs"
	"github.com/juju/juju/mongo"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/stateenvirons"
)

type Model struct {
	Name     string
	UUID     string
	Machines []Machine
}

type Machine struct {
	Tag     names.MachineTag
	Address string
}

type FlatMachine struct {
	Model   string
	Tag     names.MachineTag
	Address string
}

type database struct {
	session     *mgo.Session
	jujuDB      *mgo.Database
	collections set.Strings
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

	collections, err := jujuDB.CollectionNames()
	if err != nil {
		return nil, errors.Trace(err)
	}

	db := &database{
		session:     session,
		jujuDB:      jujuDB,
		collections: set.NewStrings(collections...),
	}
	return db, nil
}

func (db *database) Close() {
	db.session.Close()
	db.session = nil
	db.jujuDB = nil
	db.collections = nil
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

// Only supports the RunTransaction method, all others panic.
func (r *liveRunner) RunTransaction(ops []txn.Op) error {
	if r.live {
		return r.runner.RunTransaction(ops)
	}
	r.ctx.Infof("RunTransaction: \n%#v", ops)
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
	logger.Debugf("connection established")

	admin := session.DB("admin")
	if err := admin.Login(info.Tag.String(), info.Password); err != nil {
		session.Close()

		return nil, errors.Annotatef(err, "cannot log in to admin database as %q", info.Tag.String())
	}
	return session, nil
}

func openBeta7DB() (*state.State, error) {
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

func getServerMachine(st *state.State) (FlatMachine, error) {
	var empty FlatMachine
	model, err := st.Model()
	if err != nil {
		return empty, err
	}

	machine, err := st.Machine("0")
	if err != nil {
		return empty, err
	}
	addr, err := machine.PublicAddress()
	if err != nil {
		return empty, errors.Trace(err)
	}

	return FlatMachine{
		Model:   model.Name(),
		Tag:     machine.MachineTag(),
		Address: addr.Value,
	}, nil
}

// returns ip addresses
func getMachines(st *state.State) ([]Model, error) {
	// NOTE: using 2.0 funcs for b7 db for convenience.
	// Most should work ok.

	models, err := st.AllModels()
	if err != nil {
		return nil, errors.Trace(err)
	}

	var result []Model
	for _, model := range models {
		m := Model{
			Name: model.Name(),
			UUID: model.UUID(),
		}
		s, err := st.ForModel(model.ModelTag())
		if err != nil {
			return nil, errors.Trace(err)
		}
		defer s.Close()
		machines, err := s.AllMachines()
		if err != nil {
			return nil, errors.Trace(err)
		}
		for _, machine := range machines {
			addr, err := machine.PublicAddress()
			if err != nil {
				return nil, errors.Trace(err)
			}
			m.Machines = append(m.Machines, Machine{
				Tag:     machine.MachineTag(),
				Address: addr.Value,
			})
		}
		result = append(result, m)
	}

	return result, nil
}

func getAllMachines(st *state.State) ([]FlatMachine, error) {
	models, err := getMachines(st)
	if err != nil {
		return nil, errors.Trace(err)
	}
	var result []FlatMachine
	for _, model := range models {
		for _, machine := range model.Machines {
			result = append(result, FlatMachine{
				Model:   model.Name,
				Tag:     machine.Tag,
				Address: machine.Address,
			})
		}
	}
	return result, nil
}

// returns ip addresses
func getOtherMachines(st *state.State) ([]FlatMachine, error) {
	models, err := getMachines(st)
	if err != nil {
		return nil, errors.Trace(err)
	}
	var result []FlatMachine
	for _, model := range models {
		for _, machine := range model.Machines {
			if model.Name == "admin" && machine.Tag == names.NewMachineTag("0") {
				continue
			}
			result = append(result, FlatMachine{
				Model:   model.Name,
				Tag:     machine.Tag,
				Address: machine.Address,
			})
		}
	}
	return result, nil
}
