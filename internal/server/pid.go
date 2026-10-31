package server

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func acquirePID(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return gerr.Wrap(gerr.Unavailable, "create pid directory", err)
	}
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			cerr := f.Close()
			if werr != nil {
				return gerr.Wrap(gerr.Unavailable, "write pid file", werr)
			}
			if cerr != nil {
				return gerr.Wrap(gerr.Unavailable, "close pid file", cerr)
			}
			return nil
		}
		if !os.IsExist(err) {
			return gerr.Wrap(gerr.Unavailable, "create pid file", err)
		}
		old, rerr := os.ReadFile(path)
		if rerr != nil {
			return gerr.Wrap(gerr.Unavailable, "read pid file", rerr)
		}
		pid, perr := strconv.Atoi(strings.TrimSpace(string(old)))
		if perr != nil || !processAlive(pid) {
			if rm := os.Remove(path); rm != nil && !os.IsNotExist(rm) {
				return gerr.Wrap(gerr.Unavailable, "remove stale pid file", rm)
			}
			continue
		}
		return gerr.Newf(gerr.AlreadyExists, "server already running (pid %d)", pid)
	}
}

func releasePID(path string) error {
	if path == "" {
		return nil
	}
	old, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return gerr.Wrap(gerr.Unavailable, "read pid file", err)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(old)))
	if perr != nil || pid != os.Getpid() {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return gerr.Wrap(gerr.Unavailable, "remove pid file", err)
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
