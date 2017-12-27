package kit

import (
	"context"

	termbox "github.com/nsf/termbox-go"
)

type Dimension struct {
	Width  int
	Height int
}

type Point struct {
	X int
	Y int
}

// Events are readable channels for the various event types.
type Events struct {
	Chars       <-chan rune
	Keys        <-chan termbox.Key
	Resizes     <-chan Dimension
	MouseCoords <-chan Point
	Errors      <-chan error
	Interrupts  <-chan bool
}

// EventChans are writeable channels for the various event types.
type EventChans struct {
	Chars       chan<- rune
	Keys        chan<- termbox.Key
	Resizes     chan<- Dimension
	MouseCoords chan<- Point
	Errors      chan<- error
	Interrupts  chan<- bool
}

// StartPollEvents starts a goroutine that polls termbox events and
// writes them to the returned channel. It explodes the single
// termbox.Event into many events, each with unique channel. This
// makes things a bit nicer and less nested when you're going to
// manage the event loop manually with a big for{select...} loop.
//
// The downside is the caller must select from every returned
// channel. Failing to receive and event from one channel will result
// in the polling loop blocking, unable to handle further events.
func StartPollEvents(ctx context.Context) Events {
	chars := make(chan rune)
	keys := make(chan termbox.Key)
	resizes := make(chan Dimension)
	mouseCoords := make(chan Point)
	errors := make(chan error)
	interrupts := make(chan bool)

	go PollEvents(ctx, EventChans{chars, keys, resizes, mouseCoords, errors, interrupts})
	return Events{chars, keys, resizes, mouseCoords, errors, interrupts}
}

func PollEvents(ctx context.Context, events EventChans) error {
	for {
		select {
		case <-ctx.Done():
			close(events.Chars)
			close(events.Keys)
			close(events.Resizes)
			return ctx.Err()
		default:
			ev := termbox.PollEvent()
			switch ev.Type {
			case termbox.EventKey:
				if ev.Ch == 0 {
					events.Keys <- ev.Key
				} else {
					events.Chars <- ev.Ch
				}
			case termbox.EventResize:
				events.Resizes <- Dimension{Width: ev.Width, Height: ev.Height}
			case termbox.EventMouse:
				events.MouseCoords <- Point{X: ev.MouseX, Y: ev.MouseY}
			case termbox.EventError:
				events.Interrupts <- true
			default:
				// FIXME send other events
			}
		}
	}
}
