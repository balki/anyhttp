package idle

import (
	"testing"
	"time"
)

func TestIdlerChan(_ *testing.T) {
	i := CreateIdler(1 * time.Second)
	<-i.Chan()
}
