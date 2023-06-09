package raft

import (
	"log"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func init() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	rand.Seed(time.Now().UnixNano())
}

type Harness struct {
	mu sync.Mutex

	// cluster is a list of all the raft servers participating in a cluster.
	cluster []*Server
	storage []*MapStorage

	// commitChans has a channel per server in cluster with the commi channel for
	// that server.
	commitChans []chan CommitEntry

	// commits at index i holds the sequence of commits made by server i so far.
	// It is populated by goroutines that listen on the corresponding commitChans
	// channel.
	commits [][]CommitEntry

	// connected has a bool per server in cluster, specifying whether this server
	// is currently connected to peers (if false, it's partitioned and no messages
	// will pass to or from it).
	connected []bool

	n int
	t *testing.T
}

// NewHarness creates a new test Harness, initialized with n servers connected
// to each other.
func NewHarness(t *testing.T, n int) *Harness {
	ns := make([]*Server, n)
	connected := make([]bool, n)
	ready := make(chan interface{})
	commitChans := make([]chan CommitEntry, n)
	storages := make([]*MapStorage, n)
	commits := make([][]CommitEntry, n)

	// Create all Servers in this cluster, assign ids and peer ids.
	for i := 0; i < n; i++ {
		peerIds := make([]int, 0)
		for p := 0; p < n; p++ {
			if p != i {
				peerIds = append(peerIds, p)
			}
		}

		storages[i] = NewMapStorage()
		commitChans[i] = make(chan CommitEntry)
		ns[i] = NewServer(i, peerIds, storages[i], ready, commitChans[i])
		ns[i].Serve()
	}

	// Connect all peers to each other.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				ns[i].ConnectToPeer(j, ns[j].GetListenAddr())
			}
		}
		connected[i] = true
	}
	close(ready)

	h := &Harness{
		cluster:     ns,
		storage:     storages,
		commitChans: commitChans,
		commits:     commits,
		connected:   connected,
		n:           n,
		t:           t,
	}

	for i := 0; i < n; i++ {
		go h.collectCommits(i)
	}
	return h
}

func (h *Harness) Shutdown() {
	for i := 0; i < h.n; i++ {
		h.cluster[i].DisconnectAll()
		h.connected[i] = false
	}
	for i := 0; i < h.n; i++ {
		h.cluster[i].Shutdown()
	}
}

func (h *Harness) CheckSingleLeader() (int, int) {
	for r := 0; r < 5; r++ {
		leaderId := -1
		leaderTerm := -1
		for i := 0; i < h.n; i++ {
			if h.connected[i] {
				_, term, isLeader := h.cluster[i].cm.Report()
				if isLeader {
					if leaderId < 0 {
						leaderId = i
						leaderTerm = term
					} else {
						h.t.Fatalf("both %d and %d think they're leaders", leaderId, i)
					}
				}
			}
		}
		if leaderId >= 0 {
			return leaderId, leaderTerm
		}
		time.Sleep(150 * time.Millisecond)
	}

	h.t.Fatalf("leader not found")
	return -1, -1
}

func (h *Harness) collectCommits(i int) {
	for c := range h.commitChans[i] {
		h.mu.Lock()
		tlog("collectCommits(%d) got %+v", i, c)
		h.commits[i] = append(h.commits[i], c)
		h.mu.Unlock()
	}
}

func tlog(format string, a ...interface{}) {
	format = "[TEST] " + format
	log.Printf(format, a...)
}
