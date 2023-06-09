package raft

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
	Term    int
	Success bool
}

func (p *RPCProxy) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	return p.cm.AppendEntries(args, reply)
}
