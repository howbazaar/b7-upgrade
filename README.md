# b7-upgrade
Upgrade from beta 7

# Process

1. copy b7-upgrade binary to controller machine 0
1. run `b7-upgrade verify-db`
  - this will use credentails in the agent configuration to connect to the database
  - will list all models, along with the machines in those models
1. run `b7-upgrade distribute-upgrader`
  - this will make sure a copy of the upgrader binary is on every host (and container)
1. run `b7-upgrade shutdown-agents`
  - this will shutdown every juju agent
1. run `b7-upgrade migrate-db`
  - this will run upgrade steps for each database change
1. run `b7-upgrade upgrade-controller`
  - this will the jujud-2.0 binary is set for the controller machine and set appropriate symlinks in the agent tools dirs
  - it will also update the agent.conf version tag, upgradeToVersion value, and add controller tag
1. run `b7-upgrade start-controller`

1. run `b7-upgrade upgrade-agents`
  - this will the jujud-2.0 binary to each agent and set appropriate symlinks in the agent tools dirs
  - it will also update the agent.conf version tag, upgradeToVersion value, and add controller tag

1. run `b7-upgrade start-agents`
  - this will start every other juju agent
1. run `b7-upgrade start-agents`


# Changes from beta 7 to rc 2

## Database changes

`git diff juju-2.0-beta7 -- state`

 * new controller config
 * new controllerUsers collection
 * new clouds collection
 * new cloudsCredentails collection
 * new globalSetings collection
 * permissions collection added
 * services collection renamed to application
 * units collection index changed s/service/application/
 * refcounts collection added
 * relations index changed s/service/application/
 * status history index changed, added "updated" field

Database collections Updated

./annotations.bson
  - everything in the annotations collection are application settings
  - need to change from service global key to appliction global key
./controllers.bson
./models.bson
./services.bson
./sequence.bson
./units.bson
 - remote "ports", "privateaddress", "publicaddress"
 - rename "service" to "application"
./usermodelname.bson

Prechecks needed

./assignUnits.bson - should be empty
./cleanups.bson - should be empty
./migrations.bson - should be empty
./ipaddresses.bson - should be empty

Database collections Checked and Unchanged
./actionnotifications.bson
./actions.bson
./bakeryStorageItems.bson - is empty
./blockdevices.bson
./charms.bson
./containerRefs.bson
./filesystems.bson
./guimetadata.bson
./guisettings.bson
./instanceData.bson
./openedPorts.bson
./spaces.bson
./relationscopes.bson
./volumes.bson

Postchecks needed
./ip.addresses.bson
 - I think that the instance poller and machine agents
   will update the ip addresses properly. Need to check
   how the update queries and replaces, but the keys seem
   to be the same.


Database collections TODO
./cloudimagemetadata.bson
 - now global, strip uuid from the front
 - remote "model-uuid" value
./constraints.bson
 - uses global key, so needs remove / add with new key
./endpointbindings.bson
 - uses global key, so needs remove / add with new key
 - remove "env-uuid"
./leases.bson
 - "service-leadership" namespace removenamed to "application-leadership"
./linklayerdevices.bson
./linklayerdevicesrefs.bson
./machines.bson
 - supported containers remove "lxc"
./managedStoredResources.bson
./meterStatus.bson
./metricsmanager.bson
./modelEntityRefs.bson
./modelUserLastConnection.bson
./modelusers.bson
./relations.bson
 - in endpoints, "servicename" -> "applicationname"
./resources.bson
 - remove "env-uuid"
 - rename "service-id" -> "application-id"
./settings.bson
 - need to remove a bunch of settings from model settings
   - maas-oauth, maas-server -> some cloud settings
   - all  "<uuid>:s#.*" ->  "<uuid>:a#.*"

./settingsrefs.bson
./sshhostkeys.bson
./statuses.bson
./statuseshistory.bson
./storageattachments.bson
./storageconstraints.bson
./storageinstances.bson
./storedResources.bson
./subnets.bson
./toolsmetadata.bson
./txns.bson
./txns.log.bson
./txns.prune.bson
./txns.stash.bson
./userLastLogin.bson
./users.bson
 - remove "deactivated"


TODO

## Xenial agent startup script changes

TODO

## Agent configuration files

Format changed from 1.18 to 2.0

New controller tag added

+controller: controller-b9e11d7e-e98e-405a-87ae-8c728ee62790

Value changes:
-upgradedToVersion: 2.0-beta7
+upgradedToVersion: 2.0-rc2.1


## Client filestore format changes

TODO