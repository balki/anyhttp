// Package idle helps to gracefully shutdown idle servers
package idle

import (
	"fmt"
	"sync/atomic"
	"time"
)

var (
	gIdler atomic.Pointer[idler]
)

func Wait(timeout time.Duration) error {
	i := CreateIdler(timeout).(*idler)
	ok := gIdler.CompareAndSwap(nil, i)
	if !ok {
		return fmt.Errorf("idler already waiting")
	}
	i.Wait()
	return nil
}

func Tick() {
	i := gIdler.Load()
	if i != nil {
		i.Tick()
	}
}

type Idler interface {
	Enter()
	Exit()
	Wait()
	Tick()
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
