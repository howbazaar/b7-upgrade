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
   - added when updating controller
 * new controllerUsers collection
 * new clouds collection
   - added when updating controller
 * new cloudsCredentails collection
   - added when updating controller
 * new globalSetings collection
   - empty in both, but need to create one
 * permissions collection added
 * services collection renamed to application
 * units collection index changed s/service/application/
 * refcounts collection added
   - refcounts set when renaming service -> application
 * relations index changed s/service/application/
 * status history index changed, added "updated" field

Database collections Updated

* ./annotations.bson
  - everything in the annotations collection are application settings
  - need to change from service global key to appliction global key
* ./cloudimagemetadata.bson
 - now global, strip uuid from the front
 - remote "model-uuid" value
* ./constraints.bson
 - uses global key, so needs remove / add with new key
* ./controllers.bson
* ./endpointbindings.bson
 - uses global key, so needs remove / add with new key
 - remove "env-uuid"
* ./leases.bson
 - "service-leadership" namespace removenamed to "application-leadership"
* ./machines.bson
 - supported containers remove "lxc"
* ./models.bson
* ./modelEntityRefs.bson
  - rename "services" -> "applications"
* ./modelusers.bson
  - remove "access"
  - add "object-uuid" which is a copy of "model-uuid"
* ./relations.bson
 - in endpoints, "servicename" -> "applicationname"
* ./resources.bson
 - remove "env-uuid"
 - rename "service-id" -> "application-id"
* ./services.bson
* ./sequence.bson
* ./settings.bson
 - need to remove a bunch of settings from model settings
   - maas-oauth, maas-server -> some cloud settings
   - all  "<uuid>:s#.*" ->  "<uuid>:a#.*"
* ./settingsrefs.bson
 - all refcounts moved to the new refcounts collection.
* ./statuses.bson
 - uses global key, so needs remove / add with new key
* ./statuseshistory.bson
 - globalkey change
* ./storageconstraints.bson
 - key changed from 's#' -> 'asc#' with charm too
* ./subnets.bson
 - "providerid" has uuid: prefix which needs to be removed.
* ./units.bson
 - remote "ports", "privateaddress", "publicaddress"
 - rename "service" to "application"
* ./usermodelname.bson
* ./users.bson
 - remove "deactivated"

Prechecks done

* ./assignUnits.bson - should be empty
* ./cleanups.bson - should be empty
* ./migrations.bson - should be empty
* ./ipaddresses.bson - should be empty
* ./storageinstances.bson
 - did change slightly, but no current use, so check empty

Database collections Checked and Unchanged
* ./actionnotifications.bson
* ./actions.bson
* ./bakeryStorageItems.bson - is empty
* ./blockdevices.bson
* ./charms.bson
* ./containerRefs.bson
* ./filesystems.bson
* ./guimetadata.bson
* ./guisettings.bson
* ./instanceData.bson
* ./linklayerdevices.bson
  - current machines won't have the bridges that juju now creates
* ./linklayerdevicesrefs.bson
* ./meterStatus.bson
* ./metricsmanager.bson
* ./modelUserLastConnection.bson
* ./openedPorts.bson
* ./relationscopes.bson
* ./sshhostkeys.bson
* ./spaces.bson
* ./storageattachments.bson
* ./storedResources.bson
* ./userLastLogin.bson
* ./volumes.bson

Postchecks needed
* ./ip.addresses.bson
 - I think that the instance poller and machine agents
   will update the ip addresses properly. Need to check
   how the update queries and replaces, but the keys seem
   to be the same.


Database collections TODO

* ./managedStoredResources.bson
 - need to add reference to the 2.0-rc/ga version

* ./toolsmetadata.bson
 - tools structure hasn't changed, but need to add a reference
   for the 2.0 rc/ga tools.


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