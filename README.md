# b7-upgrade
Upgrade from beta 7

# Process

1. copy b7-upgrade binary to controller machine 0
1. run `b7-upgrade verify-db`
  - this will use credentails in the agent configuration to connect to the database
  - will list all models, along with the machines and units in those models
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