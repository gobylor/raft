package raft

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

type ConsensusModule struct {
	mu sync.Mutex

	id      int
	peerIds []int
	server  *Server

	currentTerm       int
	votedFor          int
	state             CMState
	electionResetTime time.Time
}

func NewConsensusModule(id int, peerIds []int, server *Server, ready <-chan interface{}) *ConsensusModule {
	cm := new(ConsensusModule)
	cm.id = id
	cm.peerIds = peerIds
	cm.server = server
	cm.state = Follower
	cm.votedFor = -1

	go func() {
		<-ready
		cm.mu.Lock()
		cm.electionResetTime = time.Now()
		cm.mu.Unlock()
		cm.runElectionTimer()
	}()

	return cm
}

func (cm *ConsensusModule) log(format string, args ...any) {
	format = fmt.Sprintf("[%d] ", cm.id) + format
	log.Printf(format, args...)
}

func (cm *ConsensusModule) Report() (id int, term int, isLeader bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.id, cm.currentTerm, cm.state == Leader
}

func (cm *ConsensusModule) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.state == Dead {
		return nil
	}

	cm.log("RequestVote: %+v [currentTerm=%d, votedFor=%d]", args, cm.currentTerm, cm.votedFor)

	if args.Term > cm.currentTerm {
		cm.log("... term out of date in RequestVote")
		cm.becomeFollower(args.Term)
	}

	if cm.currentTerm == args.Term && (cm.votedFor == -1 || cm.votedFor == args.CandidateId) {
		reply.VoteGranted = true
		cm.votedFor = args.CandidateId
		cm.electionResetTime = time.Now()
	} else {
		reply.VoteGranted = false
	}
	reply.Term = cm.currentTerm
	cm.log("... RequestVote reply: %+v", reply)
	return nil
}

func (cm *ConsensusModule) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.state == Dead {
		return nil
	}
	cm.log("AppendEntries: %+v [currentTerm=%d]", args, cm.currentTerm)

	if args.Term > cm.currentTerm {
		cm.log("... term out of date in AppendEntries")
		cm.becomeFollower(args.Term)
	}

	reply.Success = false
	if args.Term == cm.currentTerm {
		if cm.state != Follower {
			cm.becomeFollower(args.Term)
		}
		cm.electionResetTime = time.Now()
		reply.Success = true
	}
	reply.Term = cm.currentTerm
	cm.log("... AppendEntries reply: %+v", reply)
	return nil
}

func (cm *ConsensusModule) electionTimeout() time.Duration {
	return time.Duration(150+rand.Intn(150)) * time.Millisecond
}

func (cm *ConsensusModule) isLeader() bool {
	return cm.state == Leader
}

func (cm *ConsensusModule) isFollower() bool {
	return cm.state == Follower
}

func (cm *ConsensusModule) isCandidate() bool {
	return cm.state == Candidate
}

func (cm *ConsensusModule) isDead() bool {
	return cm.state == Dead
}

func (cm *ConsensusModule) Stop() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.state = Dead
	cm.log("becomes Dead")
}

func (cm *ConsensusModule) runElectionTimer() {
	electionTimeout := cm.electionTimeout()
	cm.mu.Lock()
	termStarted := cm.currentTerm
	cm.mu.Unlock()
	cm.log("election timer started (%v), term (%d)", electionTimeout, termStarted)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C

		cm.mu.Lock()
		if !cm.isFollower() && !cm.isCandidate() {
			cm.log("in election timer state=%s, bailing out", cm.state)
			cm.mu.Unlock()
			return
		}

		if cm.currentTerm != termStarted {
			cm.log("in election timer term changed from %d to %d, bailing out", termStarted, cm.currentTerm)
			cm.mu.Unlock()
			return
		}

		if elapsed := time.Since(cm.electionResetTime); elapsed >= electionTimeout {
			cm.startElection()
			cm.mu.Unlock()
			return
		}
		cm.mu.Unlock()
	}
}

func (cm *ConsensusModule) startElection() {
	cm.state = Candidate
	cm.currentTerm += 1
	preCurrentTerm := cm.currentTerm
	cm.votedFor = cm.id
	cm.electionResetTime = time.Now()
	cm.log("becomes Candidate (currentTerm=%d)", cm.currentTerm)

	votesReceived := 1

	args := RequestVoteArgs{
		Term:        preCurrentTerm,
		CandidateId: cm.id,
	}
	for _, peerId := range cm.peerIds {
		go func(peerId int) {
			var reply RequestVoteReply
			cm.log("sending RequestVote to %d: %+v", peerId, args)
			err := cm.server.Call(peerId, "ConsensusModule.RequestVote", args, &reply)
			if err == nil {
				cm.mu.Lock()
				defer cm.mu.Unlock()
				cm.log("received RequestVoteReply %+v", reply)
				if !cm.isCandidate() {
					cm.log("while waiting for reply, state = %s", cm.state)
					return
				}
				if reply.Term > preCurrentTerm {
					cm.log("term out of date in RequestVoteReply")
					cm.becomeFollower(reply.Term)
					return
				}
				if reply.Term == preCurrentTerm && reply.VoteGranted {
					votesReceived += 1
					if votesReceived*2 > len(cm.peerIds)+1 {
						cm.log("wins election with %d votes", votesReceived)
						cm.startLeader()
						return
					}
				}
			}
		}(peerId)
	}

	go cm.runElectionTimer()
}

func (cm *ConsensusModule) becomeFollower(term int) {
	cm.log("becomes Follower term=(%d)", term)
	cm.state = Follower
	cm.currentTerm = term
	cm.votedFor = -1
	cm.electionResetTime = time.Now()

	go cm.runElectionTimer()
}

func (cm *ConsensusModule) startLeader() {
	cm.log("becomes Leader (currentTerm=%d)", cm.currentTerm)
	cm.state = Leader

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			cm.leaderHeartbeats()
			<-ticker.C

			cm.mu.Lock()
			if !cm.isLeader() {
				cm.mu.Unlock()
				return
			}
			cm.mu.Unlock()
		}
	}()
}

func (cm *ConsensusModule) leaderHeartbeats() {
	cm.mu.Lock()
	if !cm.isLeader() {
		cm.mu.Unlock()
		return
	}
	savedCurrentTerm := cm.currentTerm
	cm.mu.Unlock()

	args := AppendEntriesArgs{
		Term:     savedCurrentTerm,
		LeaderId: cm.id,
	}
	for _, peerId := range cm.peerIds {
		go func(peerId int) {
			var reply AppendEntriesReply
			cm.log("sending AppendEntries to %d: %+v", peerId, args)
			err := cm.server.Call(peerId, "ConsensusModule.AppendEntries", args, &reply)
			if err == nil {
				cm.mu.Lock()
				defer cm.mu.Unlock()
				cm.log("received AppendEntriesReply %+v", reply)
				if !cm.isLeader() {
					cm.log("while waiting for reply, state = %s", cm.state)
					return
				}
				if reply.Term > savedCurrentTerm {
					cm.log("term out of date in AppendEntriesReply")
					cm.becomeFollower(reply.Term)
					return
				}
			}
		}(peerId)
	}
}
