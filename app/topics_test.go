package app_test

import (
	"testing"

	"poople/app"
)

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestTopicWakesOnlyTheNotifiedKey(t *testing.T) {
	t.Parallel()
	var topic app.Topic
	one, two := topic.Changes(1), topic.Changes(2)
	if topic.Changes(1) != one {
		t.Fatal("waiters on one key got different channels")
	}
	topic.Notify(1)
	if !closed(one) || closed(two) {
		t.Fatalf("after Notify(1): key 1 closed=%v, key 2 closed=%v", closed(one), closed(two))
	}
	if next := topic.Changes(1); closed(next) {
		t.Fatal("a new waiter saw the old notification")
	}
	topic.Notify(3) // no waiters
}
