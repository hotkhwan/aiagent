package world

import (
	"context"
	"testing"
)

func TestSnapshotCarriesTheVersion(t *testing.T) {
	e := newEnv(t)
	if got := e.col.Snapshot(context.Background()).Version; got != Version {
		t.Errorf("snapshot version = %q; want %q", got, Version)
	}
}
