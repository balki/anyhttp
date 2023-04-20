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
	"syscall"
)

type UnixSocketConfig struct {
	SocketPath     string
	SocketMode     fs.FileMode
	RemoveExisting bool
}

var DefaultUnixSocketConfig = UnixSocketConfig{
	SocketMode:     0666,
	RemoveExisting: true,
}

func NewUnixSocketConfig(socketPath string) UnixSocketConfig {
	usc := DefaultUnixSocketConfig
	usc.SocketPath = socketPath
	return usc
}

type SysdConfig struct {
	FDIndex  *int
	FDName   *string
	CheckPID bool
	UnsetEnv bool
}

var DefaultSysdConfig = SysdConfig{
	CheckPID: true,
	UnsetEnv: false,
}

func NewSysDConfigWithFDIdx(fdIdx int) SysdConfig {
	sysc := DefaultSysdConfig
	sysc.FDIndex = &fdIdx
	return sysc
}

func NewSysDConfigWithFDName(fdName string) SysdConfig {
	sysc := DefaultSysdConfig
	sysc.FDName = &fdName
	return sysc
}

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

func (s *SysdConfig) GetListener() (net.Listener, error) {

	if s.UnsetEnv {
		defer UnsetSystemdListenVars()
	}

	if s.CheckPID {
		pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
		if err != nil {
			return nil, err
		}
		if pid != os.Getpid() {
			return nil, fmt.Errorf("fd not for you")
		}
	}

	numFds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil {
		return nil, err
	}

	fdNames := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")

	if s.FDIndex != nil {
		idx := *s.FDIndex
		if idx < 0 || idx >= numFds {
			return nil, fmt.Errorf("invalid fd")
		}
		fd := StartFD + idx
		if idx < len(fdNames) {
			return makeFdListener(fd, fdNames[idx])
		} else {
			return makeFdListener(fd, fmt.Sprintf("sysdfd_%d", fd))
		}
	}

	if s.FDName != nil {
		for idx, name := range fdNames {
			if name == *s.FDName {
				fd := StartFD + idx
				return makeFdListener(fd, name)
			}
		}
		return nil, fmt.Errorf("fdName not found: %q", *s.FDName)
	}

	return nil, fmt.Errorf("neither FDIndex nor FDName set")
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
			return nil, err
		}
		sysdc := NewSysDConfigWithFDIdx(idx)
		return sysdc.GetListener()
	}

	if strings.HasPrefix(addr, "sysd/fdname/") {
		sysdc := NewSysDConfigWithFDName(strings.TrimPrefix(addr, "sysd/fdname/"))
		return sysdc.GetListener()
	}

	return nil, nil
}

func ListenAndServe(addr string, h http.Handler) error {

	listener, err := GetListener(addr)
	if err != nil {
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

func UnsetSystemdListenVars() {
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
}
