package anyhttp

import (
	"errors"
	"net"
)

func makeFdListener(fd int, name string) (net.Listener, error) {
	return nil, errors.New("windows not supported")
}
