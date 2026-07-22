//go:build !linux

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	daemonservice "github.com/kardianos/service"
)

// platformService manages the resident sync worker through the OS service
// manager (launchd, Windows service). Linux uses a systemd timer instead —
// see service_linux.go.
func platformService(action string, flags workerFlags, userService bool) int {
	service, err := newService(flags, userService)
	if err != nil {
		return fail(defaultLogger(), err)
	}

	switch action {
	case "install":
		err = service.Install()
	case "uninstall":
		err = service.Uninstall()
	case "start":
		err = service.Start()
	case "stop":
		err = service.Stop()
	case "restart":
		err = service.Restart()
	case "status":
		var status daemonservice.Status
		status, err = service.Status()
		if err == nil {
			fmt.Fprintln(os.Stdout, serviceStatusString(status))
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service action %q\n", action)
		return 2
	}
	if err != nil {
		return fail(defaultLogger(), err)
	}
	return 0
}

func newService(flags workerFlags, userService bool) (daemonservice.Service, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	config := &daemonservice.Config{
		Name:        "tokitoki",
		DisplayName: "TokiToki",
		Description: "Sync local AI usage to TokiToki.",
		Executable:  executable,
		Arguments:   serviceArguments(flags),
		Option: daemonservice.KeyValue{
			"UserService": userService,
			"Restart":     "always",
		},
	}
	return daemonservice.New(&serviceProgram{flags: flags}, config)
}

type serviceProgram struct {
	flags  workerFlags
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *serviceProgram) Start(_ daemonservice.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		_ = runWorkerLoop(ctx, p.flags)
	}()
	return nil
}

func (p *serviceProgram) Stop(_ daemonservice.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func serviceStatusString(status daemonservice.Status) string {
	switch status {
	case daemonservice.StatusRunning:
		return "running"
	case daemonservice.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
