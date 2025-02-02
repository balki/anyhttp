// Package anyhttp has helpers to serve http from unix sockets and systemd socket activated fds
package anyhttp

import (
	"context"
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

	"go.balki.me/anyhttp/idle"
)

// AddressType of the address passed
type AddressType string

var (
	// UnixSocket - address is a unix socket, e.g. unix?path=/run/foo.sock
	UnixSocket AddressType = "UnixSocket"
	// SystemdFD - address is a systemd fd, e.g. sysd?name=myapp.socket
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

// GetListener is low level function for use with non-http servers. e.g. tcp, smtp
// Caller should handle idle timeout if needed
func GetListener(addr string) (net.Listener, AddressType, any /* cfg */, error) {

	addrType, unixSocketConfig, sysdConfig, perr := parseAddress(addr)
	if perr != nil {
		return nil, Unknown, nil, perr
	}
	if unixSocketConfig != nil {
		listener, err := unixSocketConfig.GetListener()
		if err != nil {
			return nil, Unknown, nil, err
		}
		return listener, addrType, unixSocketConfig, nil
	} else if sysdConfig != nil {
		listener, err := sysdConfig.GetListener()
		if err != nil {
			return nil, Unknown, nil, err
		}
		return listener, addrType, sysdConfig, nil
	}
	if addr == "" {
		addr = ":http"
	}
	listener, err := net.Listen("tcp", addr)
	return listener, TCP, nil, err
}

type ServerCtx struct {
	AddressType      AddressType
	Listener         net.Listener
	Server           *http.Server
	Idler            idle.Idler
	Done             <-chan error
	UnixSocketConfig *UnixSocketConfig
	SysdConfig       *SysdConfig
}

func (s *ServerCtx) Wait() error {
	return <-s.Done
}

func (s *ServerCtx) Addr() net.Addr {
	return s.Listener.Addr()
}

func (s *ServerCtx) Shutdown(ctx context.Context) error {
	err := s.Server.Shutdown(ctx)
	if err != nil {
		return err
	}
	return <-s.Done
}

// ServeTLS creates and serves a HTTPS server.
func ServeTLS(addr string, h http.Handler, certFile string, keyFile string) (*ServerCtx, error) {
	return serve(addr, h, certFile, keyFile)
}

// Serve creates and serves a HTTP server.
func Serve(addr string, h http.Handler) (*ServerCtx, error) {
	return serve(addr, h, "", "")
}

// ListenAndServe is the drop-in replacement for `http.ListenAndServe`.
// Supports unix and systemd sockets in addition
func ListenAndServe(addr string, h http.Handler) error {
	ctx, err := Serve(addr, h)
	if err != nil {
		return err
	}
	return ctx.Wait()
}

func ListenAndServeTLS(addr string, certFile string, keyFile string, h http.Handler) error {
	ctx, err := ServeTLS(addr, h, certFile, keyFile)
	if err != nil {
		return err
	}
	return ctx.Wait()
}

// UnsetSystemdListenVars unsets the LISTEN* environment variables so they are not passed to any child processes
func UnsetSystemdListenVars() {
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
}

func parseAddress(addr string) (addrType AddressType, usc *UnixSocketConfig, sysc *SysdConfig, err error) {
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
			if len(val) != 1 {
				err = fmt.Errorf("unix socket address error. Multiple %v found: %v", key, val)
				return
			}
			if key == "path" {
				usc.SocketPath = val[0]
			} else if key == "mode" {
				if _, serr := fmt.Sscanf(val[0], "%o", &usc.SocketMode); serr != nil {
					err = fmt.Errorf("unix socket address error. Bad mode: %v, err: %w", val, serr)
					return
				}
			} else if key == "remove_existing" {
				if removeExisting, berr := strconv.ParseBool(val[0]); berr == nil {
					usc.RemoveExisting = removeExisting
				} else {
					err = fmt.Errorf("unix socket address error. Bad remove_existing: %v, err: %w", val, berr)
					return
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
			if len(val) != 1 {
				err = fmt.Errorf("systemd socket fd address error. Multiple %v found: %v", key, val)
				return
			}
			if key == "name" {
				sysc.FDName = &val[0]
			} else if key == "idx" {
				if idx, ierr := strconv.Atoi(val[0]); ierr == nil {
					sysc.FDIndex = &idx
				} else {
					err = fmt.Errorf("systemd socket fd address error. Bad idx: %v, err: %w", val, ierr)
					return
				}
			} else if key == "check_pid" {
				if checkPID, berr := strconv.ParseBool(val[0]); berr == nil {
					sysc.CheckPID = checkPID
				} else {
					err = fmt.Errorf("systemd socket fd address error. Bad check_pid: %v, err: %w", val, berr)
					return
				}
			} else if key == "unset_env" {
				if unsetEnv, berr := strconv.ParseBool(val[0]); berr == nil {
					sysc.UnsetEnv = unsetEnv
				} else {
					err = fmt.Errorf("systemd socket fd address error. Bad unset_env: %v, err: %w", val, berr)
					return
				}
			} else if key == "idle_timeout" {
				if timeout, terr := time.ParseDuration(val[0]); terr == nil {
					sysc.IdleTimeout = &timeout
				} else {
					err = fmt.Errorf("systemd socket fd address error. Bad idle_timeout: %v, err: %w", val, terr)
					return
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
	} else {
		// Just assume as TCP address
		return TCP, nil, nil, nil
	}
	return
}

func serve(addr string, h http.Handler, certFile string, keyFile string) (*ServerCtx, error) {

	serveFn := func() func(ctx *ServerCtx) error {
		if certFile != "" {
			return func(ctx *ServerCtx) error {
				return ctx.Server.ServeTLS(ctx.Listener, certFile, keyFile)
			}
		}
		return func(ctx *ServerCtx) error {
			return ctx.Server.Serve(ctx.Listener)
		}
	}()
	var ctx ServerCtx
	var err error
	var cfg any

	ctx.Listener, ctx.AddressType, cfg, err = GetListener(addr)
	if err != nil {
		return nil, err
	}
	switch ctx.AddressType {
	case UnixSocket:
		ctx.UnixSocketConfig = cfg.(*UnixSocketConfig)
	case SystemdFD:
		ctx.SysdConfig = cfg.(*SysdConfig)
	}
	errChan := make(chan error)
	ctx.Done = errChan
	if ctx.AddressType == SystemdFD && ctx.SysdConfig.IdleTimeout != nil {
		ctx.Idler = idle.CreateIdler(*ctx.SysdConfig.IdleTimeout)
		ctx.Server = &http.Server{Handler: idle.WrapIdlerHandler(ctx.Idler, h)}
		waitErrChan := make(chan error)
		go func() {
			waitErrChan <- serveFn(&ctx)
		}()
		go func() {
			select {
			case err := <-waitErrChan:
				errChan <- err
			case <-ctx.Idler.Chan():
				errChan <- ctx.Server.Shutdown(context.TODO())
			}
		}()
	} else {
		ctx.Server = &http.Server{Handler: h}
		go func() {
			errChan <- serveFn(&ctx)
		}()
	}
	return &ctx, nil
}
