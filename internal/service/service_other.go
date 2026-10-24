//go:build !windows

package service

import (
	"context"
	"errors"
	"io"
	"os"
)

func isAdmin() bool {
	return os.Geteuid() == 0
}

func installWindowsService(string, []string) error {
	return errors.New("not Windows")
}

func uninstallWindowsService() error {
	return errors.New("not Windows")
}

// Run reports false: only Windows starts the program as a service that
// must be talked to.
func Run(func(ctx context.Context, args []string, output io.Writer) error) bool {
	return false
}
