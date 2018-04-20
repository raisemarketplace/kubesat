package logger

import (
	"context"
	"fmt"

	"github.com/raisemarketplace/kubesat/notify"
)

type atRequest struct {
	Index     int
	ReplyChan chan<- Message
}

// Logger is a logging sink that stores a fixed number of log
// messages. New messages will overwrite the oldest message; messages
// are stored in a circular buffer.
//
// Caller is notified of new logs via Updated, which can optionally be
// subscribed to.
type Logger struct {
	Updated *notify.T

	input       chan<- Message
	atRequests  chan<- atRequest
	lenRequests chan<- chan<- int

	messages []Message
	first    int
}

func New(ctx context.Context, capacity int) *Logger {
	input := make(chan Message)
	atRequests := make(chan atRequest)
	lenRequests := make(chan chan<- int)

	logger := &Logger{
		Updated:     notify.New(ctx),
		input:       input,
		atRequests:  atRequests,
		lenRequests: lenRequests,
		messages:    make([]Message, 0, capacity),
		first:       0,
	}

	go func() {
		for {
			select {
			case message, ok := <-input:
				if !ok {
					return
				}
				logger.append(message)
			case request, ok := <-atRequests:
				if !ok {
					continue
				}
				request.ReplyChan <- logger.at(request.Index)
			case replyChan, ok := <-lenRequests:
				if !ok {
					continue
				}
				replyChan <- len(logger.messages)
			}
		}
	}()

	return logger
}

func (logger *Logger) Len() int {
	replyChan := make(chan int)
	logger.lenRequests <- replyChan
	reply, ok := <-replyChan
	if !ok {
		return 0
	}
	return reply

}

func (logger *Logger) At(i int) Message {
	replyChan := make(chan Message)
	request := atRequest{Index: i, ReplyChan: replyChan}
	logger.atRequests <- request
	reply, ok := <-replyChan
	if !ok {
		return Message{Message: "internal error: bad reply channel", Level: Error}
	}
	return reply
}

func (logger *Logger) append(m Message) {
	if len(logger.messages) == cap(logger.messages) {
		logger.messages[logger.first] = m
		logger.first = (logger.first + 1) % len(logger.messages)
	} else {
		logger.messages = append(logger.messages, m)
	}

	logger.Updated.Broadcast()
}

func (logger *Logger) at(i int) Message {
	return logger.messages[(logger.first+i)%len(logger.messages)]
}

func (logger *Logger) logf(level LogLevel, format string, args ...interface{}) {
	logger.input <- Message{level, fmt.Sprintf(format, args...)}
}

func (logger *Logger) Infof(format string, args ...interface{}) {
	logger.logf(Info, format, args...)
}

func (logger *Logger) Warnf(format string, args ...interface{}) {
	logger.logf(Warn, format, args...)
}

func (logger *Logger) Errorf(format string, args ...interface{}) {
	logger.logf(Error, format, args...)
}
