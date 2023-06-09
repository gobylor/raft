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

	commitChan         chan<- CommitEntry
	newCommitReadyChan chan struct{}

	log         []LogEntry
	commitIndex int
	lastApplied int

	nextIndex  map[int]int
	matchIndex map[int]int
}

type CommitEntry struct {
	Command interface{}
	Index   int
	Term    int
}

type LogEntry struct {
	Command interface{}
	Term    int
}

func NewConsensusModule(id int, peerIds []int, server *Server, ready <-chan interface{}, commitChan chan<- CommitEntry) *ConsensusModule {
	cm := new(ConsensusModule)
	cm.id = id
	cm.peerIds = peerIds
	cm.server = server
	cm.state = Follower
	cm.votedFor = -1
	cm.commitChan = commitChan
	cm.newCommitReadyChan = make(chan struct{}, 16)
	cm.commitIndex = -1
	cm.lastApplied = -1
	cm.nextIndex = make(map[int]int)
	cm.matchIndex = make(map[int]int)

	go func() {
		<-ready
		cm.mu.Lock()
		cm.electionResetTime = time.Now()
		cm.mu.Unlock()
		cm.runElectionTimer()
	}()

	go cm.commitChanSender()
	return cm
}

func (cm *ConsensusModule) debug(format string, args ...any) {
	format = fmt.Sprintf("[%d] ", cm.id) + format
	log.Printf(format, args...)
}

func (cm *ConsensusModule) Submit(command interface{}) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.debug("Submit received by %v: %v", cm.state, command)
	if cm.state == Leader {
		cm.log = append(cm.log, LogEntry{command, cm.currentTerm})
		cm.debug("... log=%v", cm.log)
		return true
	}
	return false
}

func (cm *ConsensusModule) Report() (id int, term int, isLeader bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.id, cm.currentTerm, cm.isLeader()
}

func (cm *ConsensusModule) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.isDead() {
		return nil
	}
	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTerm()
	cm.debug("RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]", args, cm.currentTerm, cm.votedFor, lastLogIndex, lastLogTerm)

	if args.Term > cm.currentTerm {
		cm.debug("... term out of date in RequestVote")
		cm.becomeFollower(args.Term)
	}

	if cm.currentTerm == args.Term && (cm.votedFor == -1 || cm.votedFor == args.CandidateId) &&
		(args.LastLogTerm > lastLogTerm || args.LastLogTerm == lastLogTerm && args.LastLogIndex >= lastLogIndex) {
		reply.VoteGranted = true
		cm.votedFor = args.CandidateId
		cm.electionResetTime = time.Now()
	} else {
		reply.VoteGranted = false
	}
	reply.Term = cm.currentTerm
	cm.debug("... RequestVote reply: %+v", reply)
	return nil
}

func (cm *ConsensusModule) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.isDead() {
		return nil
	}
	cm.debug("AppendEntries: %+v [currentTerm=%d]", args, cm.currentTerm)

	if args.Term > cm.currentTerm {
		cm.debug("... term out of date in AppendEntries")
		cm.becomeFollower(args.Term)
	}

	reply.Success = false
	if args.Term == cm.currentTerm {
		if !cm.isFollower() {
			cm.becomeFollower(args.Term)
		}
		cm.electionResetTime = time.Now()
		if args.PrevLogIndex == -1 || (args.PrevLogIndex < len(cm.log) && args.PrevLogTerm == cm.log[args.PrevLogIndex].Term) {
			reply.Success = true
			logInsertIndex := args.PrevLogIndex + 1
			newEntriesIndex := 0

			for {
				if logInsertIndex >= len(cm.log) || newEntriesIndex >= len(args.Entries) {
					break
				}
				if cm.log[logInsertIndex].Term != args.Entries[newEntriesIndex].Term {
					break
				}
				logInsertIndex++
				newEntriesIndex++
			}

			if newEntriesIndex < len(args.Entries) {
				cm.debug("... AppendEntries: appending entries %v from index %d", args.Entries[newEntriesIndex:], logInsertIndex)
				cm.log = append(cm.log[:logInsertIndex], args.Entries[newEntriesIndex:]...)
				cm.debug("... AppendEntries: log=%v", cm.log)
			}
			if args.LeaderCommit > cm.commitIndex {
				cm.commitIndex = intMin(args.LeaderCommit, len(cm.log)-1)
				cm.debug("... AppendEntries: setting commitIndex=%d", cm.commitIndex)
				cm.newCommitReadyChan <- struct{}{}
			}
		}
	}
	reply.Term = cm.currentTerm
	cm.debug("AppendEntries reply: %+v", reply)
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
	cm.debug("becomes Dead")
}

func (cm *ConsensusModule) runElectionTimer() {
	electionTimeout := cm.electionTimeout()
	cm.mu.Lock()
	termStarted := cm.currentTerm
	cm.mu.Unlock()
	cm.debug("election timer started (%v), term (%d)", electionTimeout, termStarted)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C

		cm.mu.Lock()
		if !cm.isFollower() && !cm.isCandidate() {
			cm.debug("in election timer state=%s, bailing out", cm.state)
			cm.mu.Unlock()
			return
		}

		if cm.currentTerm != termStarted {
			cm.debug("in election timer term changed from %d to %d, bailing out", termStarted, cm.currentTerm)
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
	cm.debug("becomes Candidate (currentTerm=%d)", cm.currentTerm)

	votesReceived := 1

	for _, peerId := range cm.peerIds {
		go func(peerId int) {
			cm.mu.Lock()
			savedLastLogIndex, savedLastLogTerm := cm.lastLogIndexAndTerm()
			cm.mu.Unlock()

			args := RequestVoteArgs{
				Term:         preCurrentTerm,
				CandidateId:  cm.id,
				LastLogIndex: savedLastLogIndex,
				LastLogTerm:  savedLastLogTerm,
			}
			var reply RequestVoteReply
			cm.debug("sending RequestVote to %d: %+v", peerId, args)
			err := cm.server.Call(peerId, "ConsensusModule.RequestVote", args, &reply)
			if err == nil {
				cm.mu.Lock()
				defer cm.mu.Unlock()
				cm.debug("received RequestVoteReply %+v", reply)
				if !cm.isCandidate() {
					cm.debug("while waiting for reply, state = %s", cm.state)
					return
				}
				if reply.Term > preCurrentTerm {
					cm.debug("term out of date in RequestVoteReply")
					cm.becomeFollower(reply.Term)
					return
				}
				if reply.Term == preCurrentTerm && reply.VoteGranted {
					votesReceived += 1
					if votesReceived*2 > len(cm.peerIds)+1 {
						cm.debug("wins election with %d votes", votesReceived)
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
	cm.debug("becomes Follower term=(%d)", term)
	cm.state = Follower
	cm.currentTerm = term
	cm.votedFor = -1
	cm.electionResetTime = time.Now()

	go cm.runElectionTimer()
}

func (cm *ConsensusModule) startLeader() {
	cm.debug("becomes Leader (currentTerm=%d)", cm.currentTerm)
	cm.state = Leader

	for _, peerId := range cm.peerIds {
		cm.nextIndex[peerId] = len(cm.log)
		cm.matchIndex[peerId] = -1
	}
	cm.debug("becomes Leader (term=%d, nextIndex=%v, matchIndex=%v), (log=%v)", cm.currentTerm, cm.nextIndex, cm.matchIndex, cm.log)

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

	for _, peerId := range cm.peerIds {
		go func(peerId int) {
			cm.mu.Lock()
			ni := cm.nextIndex[peerId]
			prevLogIndex := ni - 1
			prevLogTerm := -1
			if prevLogIndex >= 0 {
				prevLogTerm = cm.log[prevLogIndex].Term
			}
			entries := cm.log[ni:]
			args := AppendEntriesArgs{
				Term:         savedCurrentTerm,
				LeaderId:     cm.id,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  prevLogTerm,
				Entries:      entries,
				LeaderCommit: cm.commitIndex,
			}
			cm.mu.Unlock()

			var reply AppendEntriesReply
			cm.debug("sending AppendEntries to %d: %+v", peerId, args)
			err := cm.server.Call(peerId, "ConsensusModule.AppendEntries", args, &reply)
			if err == nil {
				cm.mu.Lock()
				defer cm.mu.Unlock()
				cm.debug("received AppendEntriesReply %+v", reply)
				//if !cm.isLeader() {
				//	cm.debug("while waiting for reply, state = %s", cm.state)
				//	return
				//}
				if reply.Term > savedCurrentTerm {
					cm.debug("term out of date in AppendEntriesReply")
					cm.becomeFollower(reply.Term)
					return
				}

				if cm.isLeader() && savedCurrentTerm == reply.Term {
					if reply.Success {
						cm.nextIndex[peerId] = ni + len(entries)
						cm.matchIndex[peerId] = cm.nextIndex[peerId] - 1
						cm.debug("AppendEntries reply from %d success: nextIndex := %d, matchIndex := %d", peerId, cm.nextIndex[peerId], cm.matchIndex[peerId])

						savedCommitIndex := cm.commitIndex
						for i := cm.commitIndex + 1; i < len(cm.log); i++ {
							if cm.log[i].Term == cm.currentTerm {
								matchCount := 1
								for _, peerId := range cm.peerIds {
									if cm.matchIndex[peerId] >= i {
										matchCount += 1
									}
								}
								if matchCount*2 > len(cm.peerIds)+1 {
									cm.commitIndex = i
								}
							}
						}
						if cm.commitIndex != savedCommitIndex {
							cm.debug("leader sets commitIndex := %d", cm.commitIndex)
							cm.newCommitReadyChan <- struct{}{}
						}
					} else {
						cm.nextIndex[peerId] = ni - 1
						cm.debug("AppendEntries reply from %d !success: decrement nextIndex to %d", peerId, cm.nextIndex[peerId])
					}
				}
			}
		}(peerId)
	}
}

func (cm *ConsensusModule) commitChanSender() {
	for range cm.newCommitReadyChan {
		cm.mu.Lock()
		savedTerm := cm.currentTerm
		savedLastApplied := cm.lastApplied
		var entries []LogEntry
		if cm.commitIndex > cm.lastApplied {
			entries = cm.log[cm.lastApplied+1 : cm.commitIndex+1]
			cm.lastApplied = cm.commitIndex
		}
		cm.mu.Unlock()
		cm.debug("commitChanSender: lastApplied := %d, commitIndex := %d, entries=%v", savedLastApplied, cm.commitIndex, entries)

		for i, entry := range entries {
			cm.commitChan <- CommitEntry{
				Index:   savedLastApplied + i + 1,
				Term:    savedTerm,
				Command: entry.Command,
			}
		}
	}
	cm.debug("commitChanSender done")
}

func (cm *ConsensusModule) lastLogIndexAndTerm() (int, int) {
	if len(cm.log) > 0 {
		lastLogIndex := len(cm.log) - 1
		return lastLogIndex, cm.log[lastLogIndex].Term
	}
	return -1, -1
}
