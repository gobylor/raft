package raft

import (
	"testing"
)

func TestElectionBasic(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	h.CheckSingleLeader()
}

func TestElectionLeaderDisconnect(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, origTerm := h.CheckSingleLeader()

	h.DisconnectPeer(origLeaderId)
	sleepMs(350)

	newLeaderId, newTerm := h.CheckSingleLeader()
	if newLeaderId == origLeaderId {
		t.Errorf("want new leader to be different from orig leader")
	}
	if newTerm <= origTerm {
		t.Errorf("want newTerm <= origTerm, got %d and %d", newTerm, origTerm)
	}
}

func TestElectionLeaderAndAnotherDisconnect(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	h.DisconnectPeer(origLeaderId)
	otherId := (origLeaderId + 1) % 3
	h.DisconnectPeer(otherId)

	sleepMs(450)
	h.CheckNoLeader()

	h.ReconnectPeer(otherId)
	h.CheckSingleLeader()
}

func TestDisconnectAllThenRestore(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	sleepMs(100)
	for i := 0; i < 3; i++ {
		h.DisconnectPeer(i)
	}
	sleepMs(450)
	h.CheckNoLeader()

	for i := 0; i < 3; i++ {
		h.ReconnectPeer(i)
	}
	h.CheckSingleLeader()
}

func TestElectionLeaderDisconnectThenReconnect(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	origLeaderId, _ := h.CheckSingleLeader()

	h.DisconnectPeer(origLeaderId)

	sleepMs(350)
	newLeaderId, newTerm := h.CheckSingleLeader()

	h.ReconnectPeer(origLeaderId)
	sleepMs(150)

	againLeaderId, againTerm := h.CheckSingleLeader()

	if newLeaderId != againLeaderId {
		t.Errorf("again leader id got %d; want %d", againLeaderId, newLeaderId)
	}
	if againTerm != newTerm {
		t.Errorf("again term got %d; want %d", againTerm, newTerm)
	}
}
