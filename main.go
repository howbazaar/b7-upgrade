// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"os"

	"github.com/juju/cmd"
)

func main() {
	ctx, err := cmd.DefaultContext()
	if err != nil {
		logger.Errorf("%v", err)
		os.Exit(1)
	}
	os.Exit(cmd.Main(&upgrade{}, ctx, os.Args[1:]))
}
