package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import "sync"
import "labrpc"
import "time"
import "math/rand"
import "sync/atomic"
import "fmt"

// import "bytes"
// import "labgob"

const (
	HeartbeatInterval    = time.Duration(120) * time.Millisecond
	ElectionTimeoutLower = time.Duration(300) * time.Millisecond
	ElectionTimeoutUpper = time.Duration(400) * time.Millisecond
)

type NodeState uint8

const (
	Follower  = NodeState(1)
	Candidate = NodeState(2)
	Leader    = NodeState(3)
)

func (s NodeState) String() string {
	switch {
	case s == Follower:
		return "Follower"
	case s == Candidate:
		return "Candidate"
	case s == Leader:
		return "Leader"
	}
	return "Unknown"
}

// should be called with a lock
func (rf *Raft) convertTo(s NodeState) {
	if s == rf.state {
		return
	}
	DPrintf("Term %d: server %d convert from %v to %v\n",
		rf.currentTerm, rf.me, rf.state, s)
	rf.state = s
	switch s {
	case Follower:
		rf.heartbeatTimer.Stop()
		rf.electionTimer.Reset(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))
		rf.votedFor = -1

	case Candidate:
		rf.startElection()

	case Leader:
		for i := range rf.nextIndex {
			// initialized to leader last log index + 1
			rf.nextIndex[i] = len(rf.logs)
		}
		for i := range rf.matchIndex {
			rf.matchIndex[i] = 0
		}

		rf.electionTimer.Stop()
		rf.broadcastHeartbeat()
		rf.heartbeatTimer.Reset(HeartbeatInterval)
	}
}

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

type LogEntry struct {
	Command interface{}
	Term    int
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	currentTerm    int         // 2A
	votedFor       int         // 2A
	electionTimer  *time.Timer // 2A
	heartbeatTimer *time.Timer // 2A
	state          NodeState   // 2A

	logs        []LogEntry    // 2B
	commitIndex int           // 2B
	lastApplied int           // 2B
	nextIndex   []int         // 2B
	matchIndex  []int         // 2B
	applyCh     chan ApplyMsg // 2B
}

func (rf Raft) String() string {
	return fmt.Sprintf("[node(%d), state(%v), term(%d), votedFor(%d)]",
		rf.me, rf.state, rf.currentTerm, rf.votedFor)
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	var term int
	var isleader bool
	// Your code here (2A).
	term = rf.currentTerm
	isleader = rf.state == Leader
	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term        int // 2A
	CandidateId int // 2A

	LastLogIndex int // 2B
	LastLogTerm  int // 2B
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int  // 2A
	VoteGranted bool // 2A
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// Your code here (2A, 2B).
	if args.Term < rf.currentTerm ||
		(args.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != args.CandidateId) {
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.convertTo(Follower)
		// do not return here.
	}

	// 2B: candidate's vote should be at least up-to-date as receiver's log
	// "up-to-date" is defined in thesis 5.4.1
	lastLogIndex := len(rf.logs) - 1
	if args.LastLogTerm < rf.logs[lastLogIndex].Term ||
		(args.LastLogTerm == rf.logs[lastLogIndex].Term && args.LastLogIndex < lastLogIndex) {
		// Receiver is more up-to-date, does not grant vote
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	rf.votedFor = args.CandidateId
	reply.Term = rf.currentTerm // not used, for better logging
	reply.VoteGranted = true
	// reset timer after grant vote
	rf.electionTimer.Reset(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))
}

type AppendEntriesArgs struct {
	Term     int // 2A
	LeaderId int // 2A

	PrevLogIndex int        // 2B
	PrevLogTerm  int        // 2B
	LogEntries   []LogEntry // 2B
	LeaderCommit int        // 2B
}

type AppendEntriesReply struct {
	Term    int  // 2A
	Success bool // 2A
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if args.Term < rf.currentTerm {
		reply.Success = false
		reply.Term = rf.currentTerm
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.convertTo(Follower)
		// do not return here.
	}

	// reset election timer even log does not match
	// args.LeaderId is the current term's Leader
	rf.electionTimer.Reset(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))

	// entries before args.PrevLogIndex might be unmatch
	// return false and ask Leader to decrement PrevLogIndex
	if len(rf.logs) < args.PrevLogIndex+1 ||
		rf.logs[args.PrevLogIndex].Term != args.PrevLogTerm {
		reply.Success = false
		reply.Term = rf.currentTerm
		return
	}

	// compare from rf.logs[args.PrevLogIndex + 1]
	unmatch_idx := -1
	for idx := range args.LogEntries {
		if len(rf.logs) < args.PrevLogIndex+2+idx ||
			rf.logs[args.PrevLogIndex+1+idx].Term != args.LogEntries[idx].Term {
			// unmatch log found
			unmatch_idx = idx
			break
		}
	}

	if unmatch_idx != -1 {
		// there are unmatch entries
		// truncate unmatch Follower entries, and apply Leader entries
		rf.logs = rf.logs[:args.PrevLogIndex+1+unmatch_idx]
		rf.logs = append(rf.logs, args.LogEntries[unmatch_idx:]...)
	}

	// Leader guarantee to have all committed entries
	// TODO: Is that possible for lastLogIndex < args.LeaderCommit?
	if args.LeaderCommit > rf.commitIndex {
		lastLogIndex := len(rf.logs) - 1
		if args.LeaderCommit <= lastLogIndex {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = lastLogIndex
		}
		rf.apply()
	}

	reply.Success = true
}

// should be called with a lock
func (rf *Raft) apply() {
	// apply all entries between lastApplied and committed
	// should be called after commitIndex updated
	if rf.commitIndex > rf.lastApplied {
		go func(start_idx int, entries []LogEntry) {
			for idx, entry := range entries {
				DPrintf("%v applies command %d on index %d", rf, entry.Command.(int), start_idx+idx)
				var msg ApplyMsg
				msg.CommandValid = true
				msg.Command = entry.Command
				msg.CommandIndex = start_idx + idx
				rf.applyCh <- msg
				// do not forget to update lastApplied index
				// this is another goroutine, so protect it with lock
				rf.mu.Lock()
				rf.lastApplied = msg.CommandIndex
				rf.mu.Unlock()
			}
		}(rf.lastApplied+1, rf.logs[rf.lastApplied+1:rf.commitIndex+1])
	}
}

// should be called with lock
func (rf *Raft) broadcastHeartbeat() {
	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		go func(server int) {
			rf.mu.Lock()
			if rf.state != Leader {
				rf.mu.Unlock()
				return
			}

			prevLogIndex := rf.nextIndex[server] - 1
			args := AppendEntriesArgs{
				Term:         rf.currentTerm,
				LeaderId:     rf.me,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  rf.logs[prevLogIndex].Term,
				LogEntries:   rf.logs[rf.nextIndex[server]:],
				LeaderCommit: rf.commitIndex,
			}
			rf.mu.Unlock()

			var reply AppendEntriesReply
			if rf.sendAppendEntries(server, &args, &reply) {
				rf.mu.Lock()
				if reply.Success {
					// successfully replicated args.LogEntries
					rf.matchIndex[server] = args.PrevLogIndex + len(args.LogEntries)
					rf.nextIndex[server] = rf.matchIndex[server] + 1

					// check if we need to update commitIndex
					// from the last log entry to committed one
					for i := len(rf.logs) - 1; i > rf.commitIndex; i-- {
						count := 0
						for _, matchIndex := range rf.matchIndex {
							if matchIndex >= i {
								count += 1
							}
						}

						if count > len(rf.peers)/2 {
							// most of nodes agreed on rf.logs[i]
							rf.commitIndex = i
							rf.apply()
							break
						}
					}

				} else {
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.convertTo(Follower)
					} else {
						// log unmatch
						rf.nextIndex[server] -= 1
						// TODO: retry now or later?
					}
				}
				rf.mu.Unlock()
			}
		}(i)
	}
}

// should be called with lock
func (rf *Raft) startElection() {
	rf.currentTerm += 1
	rf.electionTimer.Reset(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))

	lastLogIndex := len(rf.logs) - 1
	args := RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  rf.logs[lastLogIndex].Term,
	}

	var voteCount int32

	for i := range rf.peers {
		if i == rf.me {
			rf.votedFor = rf.me
			atomic.AddInt32(&voteCount, 1)
			continue
		}
		go func(server int) {
			var reply RequestVoteReply
			if rf.sendRequestVote(server, &args, &reply) {
				rf.mu.Lock()
				DPrintf("%v got RequestVote response from node %d, VoteGranted=%v, Term=%d",
					rf, server, reply.VoteGranted, reply.Term)
				if reply.VoteGranted && rf.state == Candidate {
					atomic.AddInt32(&voteCount, 1)
					if atomic.LoadInt32(&voteCount) > int32(len(rf.peers)/2) {
						rf.convertTo(Leader)
					}
				} else {
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.convertTo(Follower)
					}
				}
				rf.mu.Unlock()
			}
		}(i)
	}
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	term, isLeader = rf.GetState()
	if isLeader {
		rf.mu.Lock()
		index = len(rf.logs)
		rf.logs = append(rf.logs, LogEntry{Command: command, Term: term})
		rf.matchIndex[rf.me] = index
		rf.nextIndex[rf.me] = index + 1
		DPrintf("%v start agreement on command %d on index %d", rf, command.(int), index)
		rf.mu.Unlock()
	}

	return index, term, isLeader
}

//
// the tester calls Kill() when a Raft instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (rf *Raft) Kill() {
	// Your code here, if desired.
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.currentTerm = 0
	rf.votedFor = -1 // voted for no one
	rf.heartbeatTimer = time.NewTimer(HeartbeatInterval)
	rf.electionTimer = time.NewTimer(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))
	rf.state = Follower

	rf.applyCh = applyCh
	rf.logs = make([]LogEntry, 1) // start from index 1
	rf.nextIndex = make([]int, len(rf.peers))
	for i := range rf.nextIndex {
		// initialized to leader last log index + 1
		rf.nextIndex[i] = len(rf.logs)
	}
	rf.matchIndex = make([]int, len(rf.peers))

	go func(node *Raft) {
		for {
			select {
			case <-rf.electionTimer.C:
				rf.mu.Lock()
				if rf.state == Follower {
					// rf.startElection() is called in conversion to Candidate
					rf.convertTo(Candidate)
				} else {
					rf.startElection()
				}
				rf.mu.Unlock()

			case <-rf.heartbeatTimer.C:
				rf.mu.Lock()
				if rf.state == Leader {
					rf.broadcastHeartbeat()
					rf.heartbeatTimer.Reset(HeartbeatInterval)
				}
				rf.mu.Unlock()
			}
		}
	}(rf)

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	return rf
}

func randTimeDuration(lower, upper time.Duration) time.Duration {
	num := rand.Int63n(upper.Nanoseconds()-lower.Nanoseconds()) + lower.Nanoseconds()
	return time.Duration(num) * time.Nanosecond
}
