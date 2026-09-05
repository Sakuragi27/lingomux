package lingomux

import "time"

// Attempt describes one completed provider attempt without exposing payload data.
type Attempt struct {
	Provider  string
	Duration  time.Duration
	Success   bool
	ErrorKind ErrorKind
}

// AttemptHook observes completed provider attempts.
type AttemptHook func(Attempt)
