package main

import (
	"bytes"

	"gopkg.in/juju/names.v2"

	"github.com/howbazaar/b7-upgrade/agent"
	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/utils"
	"github.com/juju/utils/ssh"
)

func getConfig() (agent.ConfigSetterWriter, error) {
	tag := names.NewMachineTag("0")
	path := agent.ConfigPath("/var/lib/juju", tag)
	return agent.ReadConfig(path)
}

type RunResult struct {
	Code   int
	Stdout string
	Stderr string
}

// runViaSSH runs script in the remote machine with address addr.
func runViaSSH(addr string, script string) (RunResult, error) {
	// This is taken from cmd/juju/ssh.go there is no other clear way to set user
	userAddr := "ubuntu@" + addr
	sshOptions := ssh.Options{}
	sshOptions.SetIdentities("/var/lib/juju/system-identity")
	userCmd := ssh.Command(userAddr, []string{"sudo", "-n", "bash", "-c " + utils.ShQuote(script)}, &sshOptions)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	userCmd.Stdout = &stdoutBuf
	userCmd.Stderr = &stderrBuf
	var result RunResult
	logger.Debugf("updating %s, script:\n%s", addr, script)
	err := userCmd.Run()
	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()
	if err != nil {
		if rc, ok := err.(*cmd.RcPassthroughError); ok {
			result.Code = rc.Code
		} else {
			return result, errors.Trace(err)
		}
	}

	return result, nil
}
