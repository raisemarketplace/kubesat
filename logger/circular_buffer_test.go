package logger

import (
	"testing"
)

func TestCircularBuffer(t *testing.T) {
	buf := NewCircularBuffer(3)

	if buf.Len() != 0 {
		t.Fatalf("buf.Len() should be %d but was %d", 0, buf.Len())
	}
	buf.Append(Message{Message: "zero"})
	buf.Append(Message{Message: "one"})
	buf.Append(Message{Message: "two"})
	if buf.Len() != 3 {
		t.Fatalf("buf.Len() should be %d but was %d", 3, buf.Len())
	}

	if buf.At(0).Message != "zero" {
		t.Fatalf("expected buf[0] %s but was %s", "zero", buf.At(0).Message)
	}

	buf.Append(Message{Message: "three"})

	if buf.At(0).Message != "one" {
		t.Fatalf("expected buf[0] %s but was %s", "one", buf.At(0).Message)
	}

	buf.Append(Message{Message: "four"})

	if buf.At(0).Message != "two" {
		t.Fatalf("expected buf[0] %s but was %s", "two", buf.At(0).Message)
	}

	buf.Append(Message{Message: "five"})

	if buf.At(0).Message != "three" {
		t.Fatalf("expected buf[0] %s but was %s", "three", buf.At(0).Message)
	}
	if buf.At(1).Message != "four" {
		t.Fatalf("expected buf[0] %s but was %s", "four", buf.At(1).Message)
	}
	if buf.At(2).Message != "five" {
		t.Fatalf("expected buf[0] %s but was %s", "five", buf.At(2).Message)
	}
	if buf.At(3).Message != "three" {
		t.Fatalf("expected buf[0] %s but was %s", "three", buf.At(3).Message)
	}
}
