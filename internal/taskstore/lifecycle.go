package taskstore

import "fmt"

// AttemptStatus is where one attempt is.
type AttemptStatus string

func (s *Store) applyLifecycle(e Event) error {
	return fmt.Errorf("unknown event type %q", e.Type)
}
