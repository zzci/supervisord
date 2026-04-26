//go:build !windows && !freebsd
// +build !windows,!freebsd

package server

import (
	"fmt"
	"syscall"
)

func (s *Supervisor) checkRequiredResources() error {
	if minfds, vErr := s.getMinRequiredRes("minfds"); vErr == nil {
		return s.checkMinLimit(syscall.RLIMIT_NOFILE, "NOFILE", minfds)
	}
	if minprocs, vErr := s.getMinRequiredRes("minprocs"); vErr == nil {
		// RPROC = 6
		return s.checkMinLimit(6, "NPROC", minprocs)
	}
	return nil

}

func (s *Supervisor) getMinRequiredRes(resourceName string) (uint64, error) {
	entry, ok := s.config.GetSupervisord()
	if !ok {
		return 0, fmt.Errorf("no supervisord section")
	}
	raw := entry.GetInt(resourceName, 0)
	if raw <= 0 {
		return 0, fmt.Errorf("no such key %s", resourceName)
	}
	return uint64(raw), nil //nolint:gosec // bounded above
}

func (s *Supervisor) checkMinLimit(resource int, resourceName string, minRequiredSource uint64) error {
	var limit syscall.Rlimit

	if syscall.Getrlimit(resource, &limit) != nil {
		return fmt.Errorf("fail to get the %s limit", resourceName)
	}

	if minRequiredSource > limit.Max {
		return fmt.Errorf("%s %d is greater than Hard limit %d", resourceName, minRequiredSource, limit.Max)
	}

	if limit.Cur >= minRequiredSource {
		return nil
	}

	limit.Cur = limit.Max
	if syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit) != nil {
		return fmt.Errorf("fail to set the %s to %d", resourceName, limit.Cur)
	}
	return nil
}
