package lingomux

import "time"

// Attempt describes one completed provider attempt without exposing payload data.
type Attempt struct {
	Provider  string
	Duration  time.Duration
	Success   bool
	ErrorKind ErrorKind
}

// AttemptHook observes completed provider attempts synchronously. Hooks should
// return quickly because they delay translation and consume the total timeout.
// Hooks may run concurrently for separate Translate calls and must synchronize
// shared state. A hook panic is recovered without affecting translation.
type AttemptHook func(Attempt)

func (hook AttemptHook) invoke(attempt Attempt) {
	if hook == nil {
		return
	}
	defer func() { _ = recover() }()
	hook(attempt)
}
