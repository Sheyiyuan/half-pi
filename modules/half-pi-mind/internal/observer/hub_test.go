package observer

import (
	"testing"
	"time"
)

func TestHubSupportsMultipleSubscribersAndCancellation(t *testing.T) {
	var hub Hub[int]
	first := make(chan int, 2)
	second := make(chan int, 2)
	unsubscribe := hub.Subscribe(func(value int) { first <- value })
	hub.Subscribe(func(value int) { second <- value })
	if !hub.Publish(1) {
		t.Fatal("first publish was dropped")
	}
	awaitHubValue(t, first, 1)
	awaitHubValue(t, second, 1)
	unsubscribe()
	if !hub.Publish(2) {
		t.Fatal("second publish was dropped")
	}
	awaitHubValue(t, second, 2)
	select {
	case value := <-first:
		t.Fatalf("cancelled subscriber received %d", value)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestHubRecoversSubscriberPanic(t *testing.T) {
	var hub Hub[string]
	hub.Subscribe(func(string) { panic("observer") })
	delivered := make(chan string, 1)
	hub.Subscribe(func(value string) { delivered <- value })
	if !hub.Publish("fact") {
		t.Fatal("publish was dropped")
	}
	awaitHubValue(t, delivered, "fact")
}

func awaitHubValue[T comparable](t *testing.T, values <-chan T, want T) {
	t.Helper()
	select {
	case got := <-values:
		if got != want {
			t.Fatalf("value=%v want=%v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("observer delivery timed out")
	}
}
