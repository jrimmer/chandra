package pkg

import "time"

type Event struct {
	Topic     string
	Payload   []byte
	Source    string    // "mqtt" | "internal" | "scheduler"
	Timestamp time.Time
}
