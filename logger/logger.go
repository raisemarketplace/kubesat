package logger

import (
	"fmt"
)

type Logger struct {
	// When new messages are appended to the log, a message will
	// be written here. This channel must be read to avoid
	// deadlock.
	Updated  chan bool
	input    chan Message
	messages CircularBuffer
}

func New(capacity int) *Logger {
	return &Logger{
		Updated:  make(chan bool),
		input:    make(chan Message),
		messages: NewCircularBuffer(capacity),
	}
}

func (logger *Logger) Len() int {
	return logger.messages.Len()
}

func (logger *Logger) At(i int) Message {
	return logger.messages.At(i)
}

func (logger *Logger) Log(m Message) {
	logger.messages.Append(m)
	logger.Updated <- true
}

func (logger *Logger) Logf(level LogLevel, format string, args ...interface{}) {
	logger.Log(Message{level, fmt.Sprintf(format, args...)})
}

func (logger *Logger) Infof(format string, args ...interface{}) {
	logger.Logf(Info, format, args...)
}

func (logger *Logger) Warnf(format string, args ...interface{}) {
	logger.Logf(Warn, format, args...)
}

func (logger *Logger) Errorf(format string, args ...interface{}) {
	logger.Logf(Error, format, args...)
}
