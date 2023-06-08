package raft

import "testing"

func TestElectionBasic(t *testing.T) {
	h := NewHarness(t, 5)
	defer h.Shutdown()

	h.CheckSingleLeader()
}
