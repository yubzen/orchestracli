package agent

import "time"

type AgentEventType int

const (
	EventThinking AgentEventType = iota
	EventPlanning
	EventReading
	EventWriting
	EventRunning
	EventReviewing
	EventWaiting
	EventDone
	EventError
)

type AgentEvent struct {
	Type    AgentEventType
	Role    Role
	Detail  string
	Payload any
	At      time.Time
}
