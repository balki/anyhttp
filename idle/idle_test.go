package idle

import (
	"testing"
	"time"
)

func TestIdlerChan(_ *testing.T) {
	i := CreateIdler(10 * time.Millisecond)
	<-i.Chan()
}

func TestGlobalIdler(t *testing.T) {
	err := Wait(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("idle.Wait failed, %v", err)
	}
	err = Wait(10 * time.Millisecond)
	if err == nil {
		t.Fatal("idle.Wait should fail when called second time")
	}
}

func TestIdlerEnterExit(t *testing.T) {
	i := CreateIdler(10 * time.Millisecond).(*idler)
	i.Enter()
	if i.active.Load() != 1 {
		t.FailNow()
	}
	i.Exit()
	if i.active.Load() != 0 {
		t.FailNow()
	}
}
