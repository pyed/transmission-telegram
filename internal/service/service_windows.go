//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/pyed/transmission-telegram/v2/internal/logfile"
)

// isAdmin reports whether transmission-telegram runs elevated, as
// installing a Windows service requires.
func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func installWindowsService(exe string, args []string) error {
	if !isAdmin() {
		return errors.New("installing a Windows service needs an administrator: run transmission-telegram -install from an elevated prompt")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(Name)
	if err == nil {
		// Reinstalling points the existing service at the new settings.
		defer service.Close()
		if err := stopWindowsService(service); err != nil {
			return err
		}
		config, err := service.Config()
		if err != nil {
			return err
		}
		config.BinaryPathName = syscall.EscapeArg(exe)
		for _, arg := range args {
			config.BinaryPathName += " " + syscall.EscapeArg(arg)
		}
		config.StartType, config.DelayedAutoStart = mgr.StartAutomatic, true
		if err := service.UpdateConfig(config); err != nil {
			return fmt.Errorf("update the %s service: %w", Name, err)
		}
	} else {
		service, err = manager.CreateService(Name, exe, mgr.Config{
			DisplayName:      "transmission-telegram",
			Description:      "Telegram bot for Transmission",
			StartType:        mgr.StartAutomatic,
			DelayedAutoStart: true,
		}, args...)
		if err != nil {
			return fmt.Errorf("create the %s service: %w", Name, err)
		}
		defer service.Close()
	}
	// Restart after any failure, such as a damaged state file.
	if err := service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 15 * time.Second}},
		uint32((24 * time.Hour).Seconds())); err != nil {
		return err
	}
	if err := service.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return err
	}
	return service.Start()
}

func uninstallWindowsService() error {
	if !isAdmin() {
		return errors.New("removing a Windows service needs an administrator: run transmission-telegram -uninstall from an elevated prompt")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(Name)
	if err != nil {
		return fmt.Errorf("no %s service is installed: %w", Name, err)
	}
	defer service.Close()
	if err := stopWindowsService(service); err != nil {
		return err
	}
	return service.Delete()
}

// stopWindowsService stops service if it runs, and waits for it to stop.
func stopWindowsService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil || status.State == svc.Stopped {
		return err
	}
	if _, err := service.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop the %s service: %w", Name, err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if status, err = service.Query(); err != nil || status.State == svc.Stopped {
			return err
		}
	}
	return fmt.Errorf("the %s service did not stop within 30s", Name)
}

// Run runs the bot under the Windows service manager, when that is what
// started the program, and reports whether it did. run runs the bot with
// the program's arguments until ctx ends, writing to output what it would
// write to the console.
func Run(run func(ctx context.Context, args []string, output io.Writer) error) bool {
	inService, err := svc.IsWindowsService()
	if err != nil || !inService {
		return false
	}
	svc.Run(Name, windowsService{run})
	return true
}

type windowsService struct {
	run func(ctx context.Context, args []string, output io.Writer) error
}

// Execute runs the bot until the service manager stops it. A service has
// no console, so what the bot would write there goes to a log next to the
// config file.
func (s windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	output := io.Discard
	if path := configPathOf(os.Args[1:]); path != "" {
		if file, err := logfile.Open(filepath.Join(filepath.Dir(path), logName), logfile.MaxSize); err == nil {
			defer file.Close()
			output = file
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, os.Args[1:], output) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintln(output, time.Now().Format(time.DateTime), "transmission-telegram:", err)
				// A failure exit makes the service manager restart it.
				return false, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}

// configPathOf returns the -config value in args.
func configPathOf(args []string) string {
	for i, arg := range args {
		switch {
		case (arg == "-config" || arg == "--config") && i+1 < len(args):
			return args[i+1]
		case len(arg) > len("-config=") && arg[:len("-config=")] == "-config=":
			return arg[len("-config="):]
		case len(arg) > len("--config=") && arg[:len("--config=")] == "--config=":
			return arg[len("--config="):]
		}
	}
	return ""
}
