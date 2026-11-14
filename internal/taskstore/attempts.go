package taskstore

import "fmt"

// liveAttempt is the attempt still holding the task, if any.
func (t *Task) liveAttempt() *Attempt {
	return nil
}

func (s *Store) applyAttempt(t *Task, e Event) error {
	return fmt.Errorf("unknown event type %q", e.Type)
}
