package logger

// CircularBuffer stores a fixed number of log messages, overwriting
// the oldest when a new message is added.
type CircularBuffer struct {
	messages []Message
	first    int
}

func NewCircularBuffer(capacity int) CircularBuffer {
	return CircularBuffer{messages: make([]Message, 0, capacity), first: 0}
}

func (cb *CircularBuffer) Len() int {
	return len(cb.messages)
}

func (cb *CircularBuffer) At(i int) Message {
	return cb.messages[cb.first+i%len(cb.messages)]
}

func (cb *CircularBuffer) Append(m Message) {
	if len(cb.messages) == cap(cb.messages) {
		cb.messages[cb.first] = m
		cb.first = (cb.first + 1) % len(cb.messages)
	} else {
		cb.messages = append(cb.messages, m)
	}
}
