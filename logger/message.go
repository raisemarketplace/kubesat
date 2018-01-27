package logger

type LogLevel int

const (
	Info  LogLevel = iota
	Warn  LogLevel = iota
	Error LogLevel = iota
)

// Message represents a simple log message.
type Message struct {
	Level   LogLevel
	Message string
}
