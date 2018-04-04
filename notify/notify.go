package notify

import (
	"context"
)

type T struct {
	ctx         context.Context
	subscribers []chan<- bool
	subscribe   chan<- chan<- bool
	notify      chan<- bool
}

func New(ctx context.Context) *T {
	subscribe := make(chan chan<- bool)
	notify := make(chan bool)

	t := &T{
		subscribers: make([]chan<- bool, 0),
		subscribe:   subscribe,
		notify:      notify,
	}

	go func() {
		defer close(subscribe)
		defer close(notify)
		for {
			select {
			case <-ctx.Done():
				return
			case ch := <-subscribe:
				t.subscribers = append(t.subscribers, ch)
			case <-notify:
				for _, ch := range t.subscribers {
					go func() { ch <- true }()
				}
			}
		}
	}()

	return t
}

func (t *T) Subscribe(ch chan<- bool) {
	t.subscribe <- ch
}

func (t *T) Broadcast() {
	t.notify <- true
}
