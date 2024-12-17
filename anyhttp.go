// Package anyhttp has helpers to serve http from unix sockets and systemd socket activated fds
package anyhttp

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AddressType of the address passed
type AddressType string

var (
	// UnixSocket - address is a unix socket, e.g. unix//run/foo.sock
	UnixSocket AddressType = "UnixSocket"
	// SystemdFD - address is a systemd fd, e.g. sysd/fdname/myapp.socket
	SystemdFD AddressType = "SystemdFD"
	// TCP - address is a TCP address, e.g. :1234
	TCP AddressType = "TCP"
	// Unknown - address is not recognized
	Unknown AddressType = "Unknown"
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
	// Shutdown http server if no requests received for below timeout
	IdleTimeout *time.Duration
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

// GetListener gets a unix or systemd socket listener
func GetListener(addr string) (AddressType, net.Listener, error) {
	if strings.HasPrefix(addr, "unix/") {
		usc := NewUnixSocketConfig(strings.TrimPrefix(addr, "unix/"))
		l, err := usc.GetListener()
		return UnixSocket, l, err
	}

	if strings.HasPrefix(addr, "sysd/fdidx/") {
		idx, err := strconv.Atoi(strings.TrimPrefix(addr, "sysd/fdidx/"))
		if err != nil {
			return Unknown, nil, fmt.Errorf("invalid fdidx, addr:%q err: %w", addr, err)
		}
		sysdc := NewSysDConfigWithFDIdx(idx)
		l, err := sysdc.GetListener()
		return SystemdFD, l, err
	}

	if strings.HasPrefix(addr, "sysd/fdname/") {
		sysdc := NewSysDConfigWithFDName(strings.TrimPrefix(addr, "sysd/fdname/"))
		l, err := sysdc.GetListener()
		return SystemdFD, l, err
	}

	if port, err := strconv.Atoi(addr); err == nil {
		if port > 0 && port < 65536 {
			addr = fmt.Sprintf(":%v", port)
		} else {
			return Unknown, nil, fmt.Errorf("invalid port: %v", port)
		}
	}

	if addr == "" {
		addr = ":http"
	}

	l, err := net.Listen("tcp", addr)
	return TCP, l, err
}

// Serve creates and serve a http server.
func Serve(addr string, h http.Handler) (AddressType, *http.Server, <-chan error, error) {
	addrType, listener, err := GetListener(addr)
	if err != nil {
		return addrType, nil, nil, err
	}
	srv := &http.Server{Handler: h}
	done := make(chan error)
	go func() {
		done <- srv.Serve(listener)
		close(done)
	}()
	return addrType, srv, done, nil
}

// ListenAndServe is the drop-in replacement for `http.ListenAndServe`.
// Supports unix and systemd sockets in addition
func ListenAndServe(addr string, h http.Handler) error {
	_, _, done, err := Serve(addr, h)
	if err != nil {
		return err
	}
	return <-done
}

// UnsetSystemdListenVars unsets the LISTEN* environment variables so they are not passed to any child processes
func UnsetSystemdListenVars() {
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
}

func ParseAddress(addr string) (addrType AddressType, usc *UnixSocketConfig, sysc *SysdConfig, err error) {
	// addrType = Unknown
	usc = nil
	sysc = nil
	err = nil
	u, err := url.Parse(addr)
	if err != nil {
		return TCP, nil, nil, nil
	}
	if u.Path == "unix" {
		duc := DefaultUnixSocketConfig
		usc = &duc
		addrType = UnixSocket
		for key, val := range u.Query() {
			if key == "path" {
				if len(val) != 1 {
					err = fmt.Errorf("unix socket address error. Multiple path found: %v", val)
					return
				}
				usc.SocketPath = val[0]
			} else if key == "mode" {
				if len(val) != 1 {
					err = fmt.Errorf("unix socket address error. Multiple mode found: %v", val)
					return
				}
				if _, serr := fmt.Sscanf(val[0], "%o", &usc.SocketMode); serr != nil {
					err = fmt.Errorf("unix socket address error. Bad mode: %v, err: %w", val, serr)
					return
				}
			} else if key == "remove_existing" {
				if len(val) != 1 {
					err = fmt.Errorf("unix socket address error. Multiple remove_existing found: %v", val)
					return
				}
				if removeExisting, berr := strconv.ParseBool(val[0]); berr != nil {
					err = fmt.Errorf("unix socket address error. Bad remove_existing: %v, err: %w", val, berr)
					return
				} else {
					usc.RemoveExisting = removeExisting
				}
			} else {
				err = fmt.Errorf("unix socket address error. Bad option; key: %v, val: %v", key, val)
				return
			}
		}
		if usc.SocketPath == "" {
			err = fmt.Errorf("unix socket address error. Missing path; addr: %v", addr)
			return
		}
	} else if u.Path == "sysd" {
		dsc := DefaultSysdConfig
		sysc = &dsc
		addrType = SystemdFD
		for key, val := range u.Query() {
			if key == "name" {
				if len(val) != 1 {
					err = fmt.Errorf("systemd socket fd address error. Multiple name found: %v", val)
					return
				}
				sysc.FDName = &val[0]
			} else if key == "idx" {
				if len(val) != 1 {
					err = fmt.Errorf("systemd socket fd address error. Multiple idx found: %v", val)
					return
				}
				if idx, ierr := strconv.Atoi(val[0]); ierr != nil {
					err = fmt.Errorf("systemd socket fd address error. Bad idx: %v, err: %w", val, ierr)
					return
				} else {
					sysc.FDIndex = &idx
				}
			} else if key == "check_pid" {
				if len(val) != 1 {
					err = fmt.Errorf("systemd socket fd address error. Multiple check_pid found: %v", val)
					return
				}
				if checkPID, berr := strconv.ParseBool(val[0]); berr != nil {
					err = fmt.Errorf("systemd socket fd address error. Bad check_pid: %v, err: %w", val, berr)
					return
				} else {
					sysc.CheckPID = checkPID
				}
			} else if key == "unset_env" {
				if len(val) != 1 {
					err = fmt.Errorf("systemd socket fd address error. Multiple unset_env found: %v", val)
					return
				}
				if unsetEnv, berr := strconv.ParseBool(val[0]); berr != nil {
					err = fmt.Errorf("systemd socket fd address error. Bad unset_env: %v, err: %w", val, berr)
					return
				} else {
					sysc.UnsetEnv = unsetEnv
				}
			} else if key == "idle_timeout" {
				if len(val) != 1 {
					err = fmt.Errorf("systemd socket fd address error. Multiple idle_timeout found: %v", val)
					return
				}
				if timeout, terr := time.ParseDuration(val[0]); terr != nil {
					err = fmt.Errorf("systemd socket fd address error. Bad idle_timeout: %v, err: %w", val, terr)
					return
				} else {
					sysc.IdleTimeout = &timeout
				}
			} else {
				err = fmt.Errorf("systemd socket fd address error. Bad option; key: %v, val: %v", key, val)
				return
			}
		}
		if (sysc.FDIndex == nil) == (sysc.FDName == nil) {
			err = fmt.Errorf("systemd socket fd address error. Exactly only one of name and idx has to be set. name: %v, idx: %v", sysc.FDName, sysc.FDIndex)
			return
		}
	}
	// Just assume as TCP address
	return TCP, nil, nil, nil
}
