// Package anyhttp has helpers to serve http from unix sockets and systemd socket activated fds
package anyhttp

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// UnixSocketConfig has the configuration for Unix socket
type UnixSocketConfig struct {

	// Absolute or relative path of socket, e.g. /run/app.sock
	SocketPath string

	// Socket file permission
	SocketMode fs.FileMode

	// Whether to delete existing socket before creating new one
	RemoveExisting bool
}

// DefaultUnixSocketConfig has defaults for UnixSocketConfig
var DefaultUnixSocketConfig = UnixSocketConfig{
	SocketMode:     0666,
	RemoveExisting: true,
}

// NewUnixSocketConfig creates a UnixSocketConfig with the default values and the socketPath passed
func NewUnixSocketConfig(socketPath string) UnixSocketConfig {
	usc := DefaultUnixSocketConfig
	usc.SocketPath = socketPath
	return usc
}

type sysdEnvData struct {
	pid        int
	fdNames    []string
	fdNamesStr string
	numFds     int
}

var sysdEnvParser = struct {
	sysdOnce sync.Once
	data     sysdEnvData
	err      error
}{}

func parse() (sysdEnvData, error) {
	p := &sysdEnvParser
	p.sysdOnce.Do(func() {
		p.data.pid, p.err = strconv.Atoi(os.Getenv("LISTEN_PID"))
		if p.err != nil {
			p.err = fmt.Errorf("invalid LISTEN_PID, err: %w", p.err)
			return
		}
		p.data.numFds, p.err = strconv.Atoi(os.Getenv("LISTEN_FDS"))
		if p.err != nil {
			p.err = fmt.Errorf("invalid LISTEN_FDS, err: %w", p.err)
			return
		}
		p.data.fdNamesStr = os.Getenv("LISTEN_FDNAMES")
		p.data.fdNames = strings.Split(p.data.fdNamesStr, ":")

	})
	return p.data, p.err
}

// SysdConfig has the configuration for the socket activated fd
type SysdConfig struct {
	// Integer value starting at 0. Either index or name is required
	FDIndex *int
	// Name configured via FileDescriptorName or the default socket file name. Either index or name is required
	FDName *string
	// Check process PID matches LISTEN_PID
	CheckPID bool
	// Unsets the LISTEN* environment variables, so they don't get passed to any child processes
	UnsetEnv bool
}

// DefaultSysdConfig has the default values for SysdConfig
var DefaultSysdConfig = SysdConfig{
	CheckPID: true,
	UnsetEnv: true,
}

// NewSysDConfigWithFDIdx creates SysdConfig with defaults and fdIdx
func NewSysDConfigWithFDIdx(fdIdx int) SysdConfig {
	sysc := DefaultSysdConfig
	sysc.FDIndex = &fdIdx
	return sysc
}

// NewSysDConfigWithFDName creates SysdConfig with defaults and fdName
func NewSysDConfigWithFDName(fdName string) SysdConfig {
	sysc := DefaultSysdConfig
	sysc.FDName = &fdName
	return sysc
}

// GetListener returns the unix socket listener
func (u *UnixSocketConfig) GetListener() (net.Listener, error) {

	if u.RemoveExisting {
		if err := os.Remove(u.SocketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}

	l, err := net.Listen("unix", u.SocketPath)
	if err != nil {
		return nil, err
	}

	if err = os.Chmod(u.SocketPath, u.SocketMode); err != nil {
		return nil, err
	}

	return l, nil
}

// StartFD is the starting file descriptor number
const StartFD = 3

func makeFdListener(fd int, name string) (net.Listener, error) {
	fdFile := os.NewFile(uintptr(fd), name)
	l, err := net.FileListener(fdFile)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	return l, nil
}

// GetListener returns the FileListener created with socketed activated fd
func (s *SysdConfig) GetListener() (net.Listener, error) {

	if s.UnsetEnv {
		defer UnsetSystemdListenVars()
	}

	envData, err := parse()
	if err != nil {
		return nil, err
	}

	if s.CheckPID {
		if envData.pid != os.Getpid() {
			return nil, fmt.Errorf("unexpected PID, current:%v, LISTEN_PID: %v", os.Getpid(), envData.pid)
		}
	}

	if s.FDIndex != nil {
		idx := *s.FDIndex
		if idx < 0 || idx >= envData.numFds {
			return nil, fmt.Errorf("invalid fd index, expected between 0 and %v, got: %v", envData.numFds, idx)
		}
		fd := StartFD + idx
		if idx < len(envData.fdNames) {
			return makeFdListener(fd, envData.fdNames[idx])
		}
		return makeFdListener(fd, fmt.Sprintf("sysdfd_%d", fd))
	}

	if s.FDName != nil {
		for idx, name := range envData.fdNames {
			if name == *s.FDName {
				fd := StartFD + idx
				return makeFdListener(fd, name)
			}
		}
		return nil, fmt.Errorf("fdName not found: %q, LISTEN_FDNAMES:%q", *s.FDName, envData.fdNamesStr)
	}

	return nil, errors.New("neither FDIndex nor FDName set")
}

// UnknownAddress Error is returned when address does not match any known syntax
type UnknownAddress struct{}

func (u UnknownAddress) Error() string {
	return "unknown address"
}

// GetListener gets a unix or systemd socket listener
func GetListener(addr string) (net.Listener, error) {
	if strings.HasPrefix(addr, "unix/") {
		usc := NewUnixSocketConfig(strings.TrimPrefix(addr, "unix/"))
		return usc.GetListener()
	}

	if strings.HasPrefix(addr, "sysd/fdidx/") {
		idx, err := strconv.Atoi(strings.TrimPrefix(addr, "sysd/fdidx/"))
		if err != nil {
			return nil, fmt.Errorf("invalid fdidx, addr:%q err: %w", addr, err)
		}
		sysdc := NewSysDConfigWithFDIdx(idx)
		return sysdc.GetListener()
	}

	if strings.HasPrefix(addr, "sysd/fdname/") {
		sysdc := NewSysDConfigWithFDName(strings.TrimPrefix(addr, "sysd/fdname/"))
		return sysdc.GetListener()
	}

	return nil, UnknownAddress{}
}

// ListenAndServe is the drop-in replacement for `http.ListenAndServe`.
// Supports unix and systemd sockets in addition
func ListenAndServe(addr string, h http.Handler) error {

	listener, err := GetListener(addr)
	if _, isUnknown := err.(UnknownAddress); err != nil && !isUnknown {
		return err
	}

	if listener != nil {
		return http.Serve(listener, h)
	}

	if port, err := strconv.Atoi(addr); err == nil {
		if port > 0 && port < 65536 {

			return http.ListenAndServe(fmt.Sprintf(":%v", port), h)
		}
		return fmt.Errorf("invalid port: %v", port)
	}

	return http.ListenAndServe(addr, h)
}

// UnsetSystemdListenVars unsets the LISTEN* environment variables so they are not passed to any child processes
func UnsetSystemdListenVars() {
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
}
