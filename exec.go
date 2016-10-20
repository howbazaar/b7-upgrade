package main

import "sync"

type DistResult struct {
	Model     string
	MachineID string
	Error     error
	Code      int
	Stdout    string
	Stderr    string
}

func parallelCall(machines []FlatMachine, script string) []DistResult {

	var (
		wg      sync.WaitGroup
		results []DistResult
		lock    sync.Mutex
	)

	for _, machine := range machines {
		wg.Add(1)
		go func(machine FlatMachine) {
			defer wg.Done()
			run, err := runViaSSH(machine.Address, script)
			result := DistResult{
				Model:     machine.Model,
				MachineID: machine.ID,
				Error:     err,
				Code:      run.Code,
				Stdout:    run.Stdout,
				Stderr:    run.Stderr,
			}
			lock.Lock()
			defer lock.Unlock()
			results = append(results, result)
		}(machine)
	}

	logger.Debugf("Waiting for copies for finish")
	wg.Wait()

	// Sort the results
	return results
}
