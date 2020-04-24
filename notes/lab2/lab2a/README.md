# Part 2A

## 简介
该部分主要是要求完成 server 选举的相关功能，暂时不牵涉到 log。重点阅读论文的 5.1 以及 5.2，结合 Figure 2 食用。

首先强调一下整体架构。在课程给出的框架体系中，`Raft` 这个结构体是每个 server 持有一个的，作为状态机的存在。每个 server 功能是完全一样的，只是状态不同而已。我们定义了三种状态：

- Follower
- Candidate
- Leader

整个运行中，只有两种 RPC:

- 请求投票的 `RequestVote`
- 修改log（也作为心跳包）的 `AppendEntries`。

他们都是通过 `sendXXX()` 来调用其他 server 的 `XXX()` 方法。

除了上述两个 RPC，我们定义了两个需要周期性检查的 timer。
- 在 timeout 时重新选举 Leader 的 `electionTimer`
- 在 timeout 时发送心跳包的 `heartbeatTimer`

## Raft 结构分析

以下内容非常重要，一个好的设计直接决定了后续的代码是否顺利。我也是在修改了好几个版本（都能正确运行）之后才选出了个人觉得最优雅的设计。[Raft Structure Advice](https://pdos.csail.mit.edu/6.824/labs/raft-structure.txt) 可以作为参考但是我个人觉得其实都是一些正确的废话。

对于每一个不同状态的 Raft 节点，他们可能有如下并发的事件：

- Follower
  - 处理 `AppendEntries`
  - 处理 `RequestVote`
  - `electionTimer` 超时

- Candidate
  - 处理 `AppendEntries`
  - 处理 `RequestVote`
  - `electionTimer` 超时
  - 发起投票 `sendRequestVote`

- Leader
  - 处理 `AppendEntries`
  - 处理 `RequestVote`
  - `electionTimer` 超时
  - `heartbeatTimer` 超时
  - 发送心跳包 `sendAppendEntries`

如何组织这些事件，并且避免 race condition 成为了最具有难度的部分。

首先，`labrpc` 的特性决定了 `AppendEntries` 和 `RequestVote` 都是在一个新的 goroutine 中处理的。我们无需关心如何去给他们安排线程，只需知道这两个函数都需要使用 mutex 保护起来。

至于两个 timer `electionTimer` 和 `heartbeatTimer`，我们可以在构造 Raft 实例时 kickoff 一个 goroutine，在其中利用 `select` 不断处理这两个 timer 的 timeout 事件。在处理的时候 **并行** 地调用 `sendAppendEntries` 和 `sendRequestVote`。

最好写一个专门的状态转换函数来处理 Raft 节点三种状态的转换。

## 代码分析

个人做了一个最小实现，暂不引入用不到的 log 相关内容。
这个实现强调的是容易理解。总共 220 行代码。

首先为 Raft 增加了必要的 field。在 lab2 A 中我们仅需要新加 5 个 fields 就足够了。不需要任何 log 相关的东西。需要注意的是在读写 `currentTerm`, `votedFor`, `state` 时都需要加锁保护。
```go
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
}
```

在构造 Raft 时，kickoff 一个 goroutine 来处理 timer 相关的事件，注意加锁。
```go
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
```

状态转换函数，注意最好由 caller 来加锁避免错误导致死锁。该函数也非常简明，需要注意这里对两个 timer 的处理。
```go
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
		rf.electionTimer.Stop()
		rf.broadcastHeartbeat()
		rf.heartbeatTimer.Reset(HeartbeatInterval)
	}
}
```

另外的业务逻辑我觉得需要着重强调的不多。就是以选举为例讲下如何并行 RPC 调用吧。
```go
// should be called with lock
func (rf *Raft) startElection() {
	rf.currentTerm += 1
	rf.electionTimer.Reset(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))

	args := RequestVoteArgs{
		Term:        rf.currentTerm,
		CandidateId: rf.me,
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
			} else {
				DPrintf("%v send request vote to %d failed", rf, server)
			}
		}(i)
	}
}
```
需要注意的是以下几点：
- 保证在外界加锁后调用 `startElection()`
- 对其他每个节点开启一个 goroutine 调用 `sendRequestVote()`，并在处理返回值时候加锁
- 注意我们维护了一个记录投票数的 `int32` 并在处理请求返回值时候用原子操作进行读写判断，以决定是否升级为 Leader。

## 总结

以上就是 lab2 part A 的内容，虽然逻辑简单，但是 lab2 part A 是最值得用心推敲的，因为代码结构是在这时候决定的。我上一个阵亡的 6.824 就是因为开始代码结构不好，导致以后越来越难改。希望大家引以为鉴。
