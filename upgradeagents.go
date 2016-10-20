package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/loggo"
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
	if len(c.args) > 1 {
		return errors.Errorf("unexpected args: %v", c.args[1:])
	}
	toolsFilename := c.args[0]

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

	ctx.Infof("Waiting for copies for finish")
	wg.Wait()

	// Sort the results

	for _, result := range results {
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

	return nil
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
		script = "set -xu\n"
	} else {
		script = "set -u\n"
	}

	script += fmt.Sprintf(`
echo mkdir -p /var/lib/juju/tools/2.0.0-xenial-amd64
echo tar --extract --gzip --file=/home/ubuntu/juju-2.0.0-xenial-amd64.tgz --directory=/var/lib/juju/tools/2.0.0-xenial-amd64

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
    echo ln -s /var/lib/juju/tools/2.0.0-xenial-amd64/jujud "/var/lib/juju/tools/2.0.0-xenial-amd64/$i"
done

cd /var/lib/juju/agents
for agent in *
do
	echo rm /var/lib/juju/tools/$agent
    echo ln -s 2.0.0-xenial-amd64 /var/lib/juju/tools/$agent

    echo cp $agent/agent.conf $agent/agent.conf.old
    echo sed -i 's/# format 1.18/# format 2.0/; s/upgradedToVersion: 2.0-beta7/upgradedToVersion: 2.0-rc1\ncontroller: %s/' $agent/agent.conf
done
`, controllerTag.String())

	if live {
		script = strings.Replace(script, "echo ", "", -1)
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
