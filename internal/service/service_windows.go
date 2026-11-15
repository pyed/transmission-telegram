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
	"unsafe"

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

// installWindowsService copies the program at source to target, in the
// service's folder, and has the service run target with args: it creates
// the service, or points the one there is at target. The service is stopped
// first, since a running program cannot be replaced.
func installWindowsService(source, target string, args []string) error {
	if !isAdmin() {
		return errors.New("installing a Windows service needs an administrator: run transmission-telegram -install from an elevated prompt")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(Name)
	exists := err == nil
	if exists {
		defer service.Close()
		if err := stopWindowsService(service); err != nil {
			return err
		}
	}
	if err := copyExecutable(source, target); err != nil {
		return fmt.Errorf("copy the program to %s: %w", target, err)
	}
	if exists {
		// Reinstalling points the existing service at the new settings.
		config, err := service.Config()
		if err != nil {
			return err
		}
		config.BinaryPathName = syscall.EscapeArg(target)
		for _, arg := range args {
			config.BinaryPathName += " " + syscall.EscapeArg(arg)
		}
		config.StartType, config.DelayedAutoStart = mgr.StartAutomatic, true
		if err := service.UpdateConfig(config); err != nil {
			return fmt.Errorf("update the %s service: %w", Name, err)
		}
	} else {
		service, err = manager.CreateService(Name, target, mgr.Config{
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

// serviceDirSecurity lets only the system and administrators into the
// service's folder and what it holds, and makes the administrators its
// owner: ProgramData lets every user create and change files in the
// folders they make there.
const serviceDirSecurity = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// secureServiceDir creates the service's folder, private to the system and
// administrators from the start. A folder already there must belong to the
// administrators or the system, as one this program made does: another
// user's could hold files of theirs that the service would use. Its
// security is then set afresh.
func secureServiceDir(dir string) error {
	sd, err := windows.SecurityDescriptorFromString(serviceDirSecurity)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	err = windows.CreateDirectory(name, &attributes)
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	current, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read who owns %s: %w", dir, err)
	}
	owner, _, err := current.Owner()
	if err != nil {
		return fmt.Errorf("read who owns %s: %w", dir, err)
	}
	if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) && !owner.IsWellKnown(windows.WinLocalSystemSid) {
		who := owner.String()
		if account, domain, _, err := owner.LookupAccount(""); err == nil {
			who = domain + `\` + account
		}
		return fmt.Errorf("%s belongs to %s rather than to the administrators, so another program may have made it "+
			"to take over the service; check what it holds, delete it, and run -install again", dir, who)
	}
	owner, _, err = sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil); err != nil {
		return fmt.Errorf("make %s private to the administrators: %w", dir, err)
	}
	return nil
}

// programDataDir is Windows' ProgramData folder. It is asked of Windows,
// as the environment variable can be set by the user, for programs that
// run as them to change.
func programDataDir() string {
	if dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0); err == nil {
		return dir
	}
	return ""
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
