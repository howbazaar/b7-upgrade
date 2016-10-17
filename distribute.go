package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/utils/ssh"
)

// distributeUpgrader will copy the b7-upgrade executable
// to each machine, and put it in /var/lib/juju.
func (c *upgrade) distributeUpgrader(ctx *cmd.Context) error {
	if len(c.args) > 0 {
		return errors.Errorf("unexpected args: %v", c.args)
	}

	st, err := openDBusingState()
	if err != nil {
		return errors.Trace(err)
	}
	defer st.Close()

	machines, err := getOtherMachines(st)
	if err != nil {
		return errors.Trace(err)
	}

	fileLocation := os.Args[0]
	logger.Debugf("Using binary %q", fileLocation)

	var (
		wg      sync.WaitGroup
		results []DistResults
		lock    sync.Mutex
	)

	for _, machine := range machines {
		wg.Add(1)
		go func(machine FlatMachine) {
			defer wg.Done()
			result := CopyToMachine(c.live, fileLocation, "/var/lib/juju/b7-upgrade", machine.Address)
			result.Model = machine.Model
			result.Machine = machine.Tag
			lock.Lock()
			defer lock.Unlock()
			results = append(results, result)
		}(machine)
	}

	ctx.Infof("Waiting for copies for finish")
	wg.Wait()

	// Sort the results

	for _, result := range results {
		ctx.Infof("%s %s", result.Model, result.Machine.Id())
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

// CopyToMachine sends the binary to the specified machine.
func CopyToMachine(live bool, filename, target, address string) DistResults {
	var results DistResults
	// First we need to scp the file to the other machine, then move it to the right place.
	basename := filepath.Base(filename)
	args := []string{
		filename, fmt.Sprintf("ubuntu@%s:~/%s", address, basename),
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

	script := fmt.Sprintf("echo sudo mv /home/ubuntu/%s %s", basename, target)
	if live {
		script = fmt.Sprintf("sudo mv /home/ubuntu/%s %s", basename, target)
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
