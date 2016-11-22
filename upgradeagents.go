package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/utils/set"
	"github.com/juju/utils/ssh"
	"gopkg.in/juju/names.v2"
)

type DistResults struct {
	Model     string
	MachineID string
	Error     error
	Code      int
	Stdout    string
	Stderr    string
}

// upgradeAgents will copy the 2.0 tools to every machines
// and unpack into the /var/lib/juju/tools dir.
// Adds symlinks for each agent, and updates agent conf files.
func (c *upgrade) upgradeAgents(ctx *cmd.Context) error {
	if len(c.args) == 0 {
		return errors.Errorf("missing path to 2.0 tools file")
	}
	toolsFilename := c.args[0]
	addresses := set.NewStrings(c.args[1:]...)

	db, err := NewDatabase()
	if err != nil {
		return errors.Trace(err)
	}
	defer db.Close()

	controllerUUID := db.ControllerUUID()
	if controllerUUID == "" {
		return errors.New("missing controller UUID, has upgrade-db been run?")
	}
	controllerTag := names.NewControllerTag(controllerUUID)

	machines, err := getAllMachines()
	if err != nil {
		return errors.Trace(err)
	}

	var (
		wg      sync.WaitGroup
		results []DistResults
		lock    sync.Mutex
	)

	for _, machine := range machines {
		if addresses.IsEmpty() || addresses.Contains(machine.Address) {
			logger.Debugf("initiate copy to %s:%s (%s)", machine.Model, machine.ID, machine.Address)
			wg.Add(1)
			go func(machine FlatMachine) {
				defer wg.Done()
				result := CopyToolsToMachine(c.live, toolsFilename, machine.Address, controllerTag)
				result.Model = machine.Model
				result.MachineID = machine.ID
				lock.Lock()
				defer lock.Unlock()
				results = append(results, result)
			}(machine)
		}
	}

	ctx.Infof("Waiting for copies for finish")
	wg.Wait()

	// Sort the results

	for _, result := range results {
		// Show all those with errors at the end.
		if result.Error != nil {
			continue
		}
		ctx.Infof("%s %s", result.Model, result.MachineID)
		if result.Code != 0 {
			ctx.Infof("  Code: %s", result.Code)
		}
		if result.Stdout != "" {
			ctx.Infof("  stdout:")
			out := strings.Join(strings.Split(result.Stdout, "\n"), "\n    ")
			ctx.Infof("    %s", out)
		}
		if result.Stderr != "" {
			ctx.Infof("  stderr:")
			out := strings.Join(strings.Split(result.Stderr, "\n"), "\n    ")
			ctx.Infof("    %s", out)
		}
	}

	var errorResult error

	for _, result := range results {
		// Only showing errors
		if result.Error == nil {
			continue
		}
		errorResult = errors.New("one or more machines had a problem")
		ctx.Infof("%s %s", result.Model, result.MachineID)
		if result.Error != nil {
			ctx.Infof("  ERROR: %v", result.Error)
		}
		if result.Code != 0 {
			ctx.Infof("  Code: %s", result.Code)
		}
		if result.Stdout != "" {
			ctx.Infof("  stdout:")
			out := strings.Join(strings.Split(result.Stdout, "\n"), "\n    ")
			ctx.Infof("    %s", out)
		}
		if result.Stderr != "" {
			ctx.Infof("  stderr:")
			out := strings.Join(strings.Split(result.Stderr, "\n"), "\n    ")
			ctx.Infof("    %s", out)
		}
	}

	return errorResult
}

func CopyToolsToMachine(live bool, filename, address string, controllerTag names.ControllerTag) DistResults {
	var results DistResults
	// First we need to scp the file to the other machine, then move it to the right place.
	args := []string{
		filename, fmt.Sprintf("ubuntu@%s:~/juju-2.0.0-xenial-amd64.tgz", address),
	}

	options := &ssh.Options{}
	options.SetIdentities("/var/lib/juju/system-identity")

	results.Stdout = fmt.Sprintf("scp %s %s", args[0], args[1])
	if live {
		err := ssh.Copy(args, options)
		if err != nil {
			results.Error = err
			return results
		}
	}

	var script string
	if logger.LogLevel() == loggo.DEBUG {
		script = "set -xeu\n"
	} else {
		script = "set -eu\n"
	}

	script += fmt.Sprintf(`
if [ ! -d /var/lib/juju/tools/2.0.0-xenial-amd64 ]; then
    echo Unpack 2.0.0 tools and create jujuc command symlinks
	do-op mkdir -p /var/lib/juju/tools/2.0.0-xenial-amd64
	do-op tar --extract --gzip --file=/home/ubuntu/juju-2.0.0-xenial-amd64.tgz --directory=/var/lib/juju/tools/2.0.0-xenial-amd64

	declare -a jujuc=(
	  "action-fail"
	  "action-get"
	  "action-set"
	  "add-metric"
	  "application-version-set"
	  "close-port"
	  "config-get"
	  "is-leader"
	  "juju-log"
	  "juju-reboot"
	  "leader-get"
	  "leader-set"
	  "network-get"
	  "opened-ports"
	  "open-port"
	  "payload-register"
	  "payload-status-set"
	  "payload-unregister"
	  "relation-get"
	  "relation-ids"
	  "relation-list"
	  "relation-set"
	  "resource-get"
	  "status-get"
	  "status-set"
	  "storage-add"
	  "storage-get"
	  "storage-list"
	  "unit-get"
	)

	for i in "${jujuc[@]}"; do
	    do-op ln -s /var/lib/juju/tools/2.0.0-xenial-amd64/jujud "/var/lib/juju/tools/2.0.0-xenial-amd64/$i"
	done
fi

cd /var/lib/juju/agents
for agent in *
do
    echo Set tools symlink for $agent
	do-op rm /var/lib/juju/tools/$agent
    do-op ln -s 2.0.0-xenial-amd64 /var/lib/juju/tools/$agent

    version=$(head -n 1 $agent/agent.conf)
    if [ "$version" = "# format 2.0" ]; then
      echo $agent/agent.conf already in format 2.0
    elif [ "$version" = "# format 1.18" ]; then
      echo Update $agent/agent.conf to be format 2.0
      do-op cp $agent/agent.conf $agent/agent.conf.old
      do-op sed -i 's/# format 1.18/# format 2.0/; s/upgradedToVersion: 2.0-beta7/upgradedToVersion: 2.0-rc1\ncontroller: %s/' $agent/agent.conf
    else
      echo $agent/agent.conf has unexpected format: $version
    fi
done
`, controllerTag.String())

	if live {
		script = strings.Replace(script, "do-op ", "", -1)
	} else {
		script = strings.Replace(script, "do-op ", "echo '  run: '", -1)
	}
	result, err := runViaSSH(address, script)
	if err != nil {
		results.Error = err
		return results
	}
	results.Code = result.Code
	results.Stderr = result.Stderr
	results.Stdout += "\n" + result.Stdout

	return results
}
