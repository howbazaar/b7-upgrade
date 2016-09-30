package rc

import (
	"errors"

	"gopkg.in/mgo.v2/bson"
)

type Life int8

const (
	Alive Life = iota
	Dying
	Dead
)

func (l Life) String() string {
	switch l {
	case Alive:
		return "alive"
	case Dying:
		return "dying"
	case Dead:
		return "dead"
	default:
		return "unknown"
	}
}

var (
	isAliveDoc = bson.D{{"life", Alive}}
	isDyingDoc = bson.D{{"life", Dying}}
	isDeadDoc  = bson.D{{"life", Dead}}
	notDeadDoc = bson.D{{"life", bson.D{{"$ne", Dead}}}}

	errDeadOrGone     = errors.New("neither alive nor dying")
	errAlreadyDying   = errors.New("already dying")
	errAlreadyDead    = errors.New("already dead")
	errAlreadyRemoved = errors.New("already removed")
	errNotDying       = errors.New("not dying")
)

// ModelDoc represents the internal state of the model in MongoDB.
type ModelDoc struct {
	UUID           string `bson:"_id"`
	Name           string
	Life           Life
	Owner          string `bson:"owner"`
	ControllerUUID string `bson:"controller-uuid"`
	MigrationMode  string `bson:"migration-mode"`

	// Cloud is the name of the cloud to which the model is deployed.
	Cloud string `bson:"cloud"`

	// CloudRegion is the name of the cloud region to which the model is
	// deployed. This will be empty for clouds that do not support regions.
	CloudRegion string `bson:"cloud-region,omitempty"`

	// CloudCredential is the ID of the cloud credential that is used
	// for managing cloud resources for this model. This will be empty
	// for clouds that do not require credentials.
	CloudCredential string `bson:"cloud-credential,omitempty"`

	// LatestAvailableTools is a string representing the newest version
	// found while checking streams for new versions.
	LatestAvailableTools string `bson:"available-tools,omitempty"`
}
