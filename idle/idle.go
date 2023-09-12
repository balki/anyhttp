// Package idle helps to gracefully shutdown idle (typically http) servers
package idle

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

var (
	// For simple servers without backgroud jobs, global singleton for simpler API
	// Enter/Exit worn't work for global idler as Enter may be called before Wait, use CreateIdler in those cases
	gIdler atomic.Pointer[idler]
)

// Wait waits till the server is idle and returns. i.e. no Ticks in last <timeout> duration
func Wait(timeout time.Duration) error {
	i := CreateIdler(timeout).(*idler)
	ok := gIdler.CompareAndSwap(nil, i)
	if !ok {
		return fmt.Errorf("idler already waiting")
	}
	i.Wait()
	return nil
}

// Tick records the current time. This will make the server not idle until next Tick or timeout
func Tick() {
	i := gIdler.Load()
	if i != nil {
		i.Tick()
	}
}

// WrapHandler calls Tick() before processing passing request to http.Handler
func WrapHandler(h http.Handler) http.Handler {
	if h == nil {
		h = http.DefaultServeMux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Tick()
		h.ServeHTTP(w, r)
	})
}

// WrapIdlerHandler calls idler.Tick() before processing passing request to http.Handler
func WrapIdlerHandler(i Idler, h http.Handler) http.Handler {
	if h == nil {
		h = http.DefaultServeMux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i.Tick()
		h.ServeHTTP(w, r)
	})
}

// Idler helps manage idle servers
type Idler interface {
	// Tick records the current time. This will make the server not idle until next Tick or timeout
	Tick()

	// Wait waits till the server is idle and returns. i.e. no Ticks in last <timeout> duration
	Wait()

	// For long running background jobs, use Enter to record start time. Wait will not return while there are active jobs running
	Enter()

	// Exit records end of a background job
	Exit()

	// Get the channel to wait yourself
	Chan() <-chan struct{}
}

type idler struct {
	lastTick atomic.Pointer[time.Time]
	c        chan struct{}
	active   atomic.Int64
}

func (i *idler) Enter() {
	i.active.Add(1)
}

func (i *idler) Exit() {
	i.Tick()
	i.active.Add(-1)
}

// CreateIdler creates an Idler with given timeout
func CreateIdler(timeout time.Duration) Idler {
	i := &idler{}
	i.c = make(chan struct{})
	i.Tick()
	go func() {
		for {
			if i.active.Load() != 0 {
				time.Sleep(timeout)
				continue
			}
			t := *i.lastTick.Load()
			now := time.Now()
			dur := t.Add(timeout).Sub(now)
			if dur == dur.Abs() {
				time.Sleep(dur)
				continue
			}
			break
		}
		close(i.c)
	}()
	return i
}

func (i *idler) Tick() {
	now := time.Now()
	i.lastTick.Store(&now)
}

func (i *idler) Wait() {
	<-i.c
}

func (i *idler) Chan() <-chan struct{} {
	return i.c
}
