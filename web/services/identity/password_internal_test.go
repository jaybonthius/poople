package identity

import (
	"errors"
	"testing"
)

// Not parallel: it takes every hashing slot, which parallel tests share.
func TestPasswordHashingIsBoundedAndAnswersBusyWhenFull(t *testing.T) {
	hash, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	for range cap(passwordSlots) {
		passwordSlots <- struct{}{}
	}
	if _, err := hashPassword("correct horse battery"); !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("hash with every slot taken: %v", err)
	}
	if _, err := passwordMatches("correct horse battery", hash); !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("check with every slot taken: %v", err)
	}
	for range cap(passwordSlots) {
		<-passwordSlots
	}
	if ok, err := passwordMatches("correct horse battery", hash); !ok || err != nil {
		t.Fatalf("check after slots free: %v %v", ok, err)
	}
}
