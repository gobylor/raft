package raft

import (
	"fmt"
	"math/rand"
	"time"
)

type RPCProxy struct {
	cm *ConsensusModule
}

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

func (p *RPCProxy) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	f := rand.Float32()
	if f < MockUnreliableRpcFailureRate {
		p.cm.debug("drop RequestVote")
		return fmt.Errorf("RPC failed")
	}
	if f < MockUnreliableRpcDelayRate {
		p.cm.debug("delay RequestVote")
		time.Sleep(time.Duration(MockUnreliableRpcDelayMin+rand.Intn(MockUnreliableRpcDelayMax-MockUnreliableRpcDelayMin)) * TimeoutUnit)
	} else {
		time.Sleep(time.Duration(MockUnreliableRpcLatencyMin+rand.Intn(MockUnreliableRpcLatencyMax-MockUnreliableRpcLatencyMin)) * TimeoutUnit)
	}

	return p.cm.RequestVote(args, reply)
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term          int
	Success       bool
	ConflictIndex int
	ConflictTerm  int
}

func (p *RPCProxy) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	f := rand.Float32()
	if f < MockUnreliableRpcFailureRate {
		p.cm.debug("drop RequestVote")
		return fmt.Errorf("RPC failed")
	}
	if f < MockUnreliableRpcDelayRate {
		p.cm.debug("delay RequestVote")
		time.Sleep(time.Duration(MockUnreliableRpcDelayMin+rand.Intn(MockUnreliableRpcDelayMax-MockUnreliableRpcDelayMin)) * TimeoutUnit)
	} else {
		time.Sleep(time.Duration(MockUnreliableRpcLatencyMin+rand.Intn(MockUnreliableRpcLatencyMax-MockUnreliableRpcLatencyMin)) * TimeoutUnit)
	}
	return p.cm.AppendEntries(args, reply)
}
