package idle

import (
	"testing"
	"time"
)

func TestIdlerChan(t *testing.T) {
	i := CreateIdler(1 * time.Second)
	<-i.Chan()
}
