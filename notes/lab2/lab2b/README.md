# Part 2B

## 简介
在 lab2 part A 中，我们实现了选举相关的逻辑，保证了无论什么情况下系统中有且仅有一个**合法的** Leader 存在（确实有可能存在多个 Leader， 比如一个 Leader 刚从错误中恢复，以 Leader 身份重连，但是它不是合法的）。
这个 Leader 将负责管理 replicated log。它从客户端接收 log entries，
然后告诉服务器什么时候可以安全地 apply 这些 log entries。
这些都只需要 Leader 自己决策而不用与其他 server 通信，这也是 Raft 的一大优势：数据流向简单。
在 lab2 part B 中我们的任务就是实现 log 相关的逻辑。其具体内容在
[论文](https://pdos.csail.mit.edu/6.824/papers/raft-extended.pdf)第 5.3 和 5.4.1 节。

## 论文要点

### 5.3 Log replication
每个 log entry 存储了一个 command 和一个 term。

当一个 log entry 被 Leader 复制到了 **大部分的** servers 上之后，
这个 log entry 就被称为 *committed*。Raft 保证一个 committed 的 entry 最终会被所有可达的节点执行。

为了使 Follower 的状态与 Leader 一致，Follower 需要找到它和 Leader 之间最后一个共同的 LogEntry，
将其后的所有 LogEntry 全部覆盖为 Leader 的。Leader 维护了所有节点的 *nextIndex*，
其含义是 Leader 需要与某个节点同步的第一个 LogEntry 的 index。
当一个 Leader 刚被选出的时候，它会把所有 *nextIndex* 初始化为自己的下一个 LogEntry index。
也就是说无需同步。这样如果有不一致的情况，Leader 发出的 `AppendEntries` RPC 会失败，
Leader 会减少 *nextIndex* 然后重试，直到一致为止。
这个机制也解释了为什么我们的 `logs` 从第 1 个开始，这样第 0 个必然是所有节点都相同的。在重试时不会越界。

#### Raft Log Matching Property
- 如果两个 entry 具有同样的 index 和 term，则他们具有同样的 command
- 如果两个 entry 具有同样的 index 和 term，则他们所在的 log 的之前所有 entry 都相同

这就是为什么我们只需要找到最后一个 index 和 term 都 match 的 LogEntry 就能确定之前所有 log 都同步。

### 5.4.1 Election restriction

在所有 Leader-Follower 一致性算法中，Leader 最终都必须存储所有的 committed entries。
Raft 保证了 **所有来自过去的 term 的 committed entries 在新的 Leader 选出的时刻就已经存在与 Leader 的 logs 中**。
其机制为在选举阶段，如果一个 Candidate 的 log 不 “up-to-date”，则不会当选。

所谓 ”up-to-date“ 是指比大部分的节点的 log 更新，这就保证其包含所有的 committed entries。
Raft 通过比较最后一个 entry 的 index 和 term 来判断谁更新。
- 谁的 term 更大谁更新
- term 一致时，谁的 log 更长谁更新



## 疑难解答

记录一些值得思考的问题。

### 如何 debug

这个 lab 中经常遇到概率性 fail 的情况。不要怀疑，肯定是自己代码的问题。那么我们如何保证自己的正确性呢，当然是用脚本重复跑 n 次了。下面是我写的一个简陋的脚本。
```bash
#!/bin/bash
set -e
if [ $# -ne 2 ]; then
	echo "Usage: $0 [test] [repeat time]"
	exit 1
fi
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/raft"
for ((i=0;i<$2;i++))
do
	go test -run $1
done
```

### nextIndex[] 和 matchIndex[] 是否冗余

虽然有的人会认为 `matchIndex[]` 就是 `nextIndex[]` 减去1， 其实不完全正确。从初始化来看，
`nextIndex[]` 被初始化为 Leader 的下一个 LogEntry 的 index。因此 `nextIndex[]` 是一个乐观估计，

我们先假设所有的 Follower 与 Leader 的 log 都能 match。此后在发送 heartbeat 时候不断调整。
同步后再更新 `matchIndex[]`，并根据它更新 `commitIndex`。因此 `matchIndex[]` 是一个保守估计。


### PrevLogIndex 如何选取

论文里的说明还是非常模糊的。

> index of log entry immediately preceding new ones

对于我来讲这说了等于没说。所谓的 "new ones" 其实是指 Leader 即将发送给某一个 Follower 的 Entries。那么 `PrevLogIndex` 其实就是指目前 Leader 认为该 Follower 已经同步的位置。因此设置为 `nextIndex[Follower]` 的前一位即可。注意这里不能使用 `matchIndex[Follower]`，因为这里我们希望用尽量 aggressive 的估计，在收到 Follower 的回复后还可以调小 `PrevLogIndex`。

### applyCh 到底用来干啥

大部分人刚开始做 lab2 part B 时，吭哧吭哧写完逻辑发现一个 test 都过不去，就是没有理解这个。
我们通过 `Start()` 提交的 LogEntry 在最终被 Raft commit 过后，会开始 apply。在测试用例中，
apply 就是利用 `applyCh` 将 `ApplyMsg` 通知出去。

### 到底什么时候加锁

这个问题如果你有一个清晰的线程设计，应该很简单。两条标准吧。

- 对 rf 结构体的 **non-const** 数据进行读写时必须加锁
  - 所谓 **non-const** 是说除了 `rf.me`, `rf.peers` 这类一直不会变的之外的成员变量
- RPC 调用一定不能加锁
  - 否则就完全不叫并行了。

如果你发现你的程序经常死锁，或者自己都搞不明白哪里该加锁。那还是反思下线程模型设计是否合理吧。

### TestBackup2B() 失败

这个 test 遇到了两个错误。非常难 debug，建议 uncomment `config.go` 中的
`connect()` `disconnect()` 两个函数的 logging 以理解 test 的场景。

#### 长期无法选举出一个 Leader
发生情形是在 `TestBackup2B` 中的以下代码：
```go
// bring original leader back to life,
for i := 0; i < servers; i++ {
  cfg.disconnect(i)
}
cfg.connect((leader1 + 0) % servers)
cfg.connect((leader1 + 1) % servers)
cfg.connect(other)

// lots of successful commands to new group.
for i := 0; i < 50; i++ {
  cfg.one(rand.Int(), 3, true)
}
```
这个过程其实是两个 partition 的情形。具体 log 如下：
```
***** disconnect(0) *****
***** disconnect(1) *****
***** disconnect(2) *****
***** disconnect(3) *****
***** disconnect(4) *****
##### connect(4) #####
##### connect(0) #####
##### connect(1) #####
2019/08/08 22:17:33 [node(4), state(Leader), term(1), votedFor(4)] start agreement on command 1611260177215287434 on index 52
2019/08/08 22:17:33 Term 38: server 4 convert from Leader to Follower
2019/08/08 22:17:34 [node(1), state(Candidate), term(5), votedFor(1)] got RequestVote response from node 4, VoteGranted=false, Term=38
2019/08/08 22:17:34 Term 38: server 1 convert from Candidate to Follower
2019/08/08 22:17:34 [node(1), state(Follower), term(38), votedFor(-1)] got RequestVote response from node 0, VoteGranted=false, Term=38
2019/08/08 22:17:34 [node(1), state(Follower), term(38), votedFor(-1)] last log term 3, index 51; candidate 0 last log term 1, index 51
2019/08/08 22:17:34 [node(4), state(Follower), term(38), votedFor(-1)] last log term 1, index 52; candidate 0 last log term 1, index 51
```
在此前，2 和 3 没有断线，但是由于只有两个节点无法达成一致(2 < 5/2)。之后节点 2 和 3 断开；0，1 和 4 重连。
此后由 0，1，4 组成的集群再也无法选举出 Leader。其每个节点 LastLogEntry 信息如下：

Node | LastLogTerm |  LastLogIndex  
-|-|-
0 | 1 | 51 |
1 | 3 | 51 |
4 | 1 | 52 |

按 Raft 的思路，此时节点 1 应该成为新的 Leader。因为它具有最新的 Log。然而经过了无数次徒劳的选举，还是没有 Leader 产生。

这个 bug 耗费了极其长的时间去修（大概3天晚上），一直以为是自己某一个 log replication 相关的逻辑没有写对，疯狂加 logging 来 debug。事实证明，真的是自己傻逼。先给个傻逼版的 `RequestVote` 实现：
```go
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
	rf.currentTerm = args.Term
	reply.VoteGranted = true
	// reset timer after grant vote
	rf.electionTimer.Reset(randTimeDuration(ElectionTimeoutLower, ElectionTimeoutUpper))
	rf.convertTo(Follower)
}
```
看得出问题吗？看不出就对了，我也看了好几个小时时间。

以刚才列举的 0，1，4 节点为例，如果 candidate 1 收到了 candidate 0 的 `RequestVote`，并且 candidate 0 的 Term 较高，按上面的逻辑，由于 0 的 log 没有 1 的新，在检查 log 后就直接返回了。这 **违背了论文的以下要求** ：

> • If RPC request or response contains term T > currentTerm: set currentTerm = T, convert to follower (§5.1)

这里有说需要成功投票再更新 term 吗？并没有呀！没有忠实实现这一条导致节点很难转成 Follower，无法重置自己的 `votedFor`。导致一直自己给自己投票并发起选举。这显然无法迅速选举出一个新的 Leader。

#### index out of range
```go
prevLogIndex := rf.nextIndex[server] - 1
args := AppendEntriesArgs{
  Term:         rf.currentTerm,
  LeaderId:     rf.me,
  PrevLogIndex: prevLogIndex,
  PrevLogTerm:  rf.logs[prevLogIndex].Term,
  LogEntries:   rf.logs[rf.nextIndex[server]:],
  LeaderCommit: rf.commitIndex,
}
```
`PrevLogTerm:  rf.logs[prevLogIndex].Term` 这一行出错，其原因是一个隐藏非常深的 race condition。

```
==================
WARNING: DATA RACE
Write at 0x00c0003184e0 by goroutine 366:
  runtime.slicecopy()
      /usr/local/go/src/runtime/slice.go:221 +0x0
  raft.(*Raft).AppendEntries()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/raft/raft.go:387 +0x97e
  runtime.call32()
      /usr/local/go/src/runtime/asm_amd64.s:522 +0x3a
  reflect.Value.Call()
      /usr/local/go/src/reflect/value.go:308 +0xc0
  labrpc.(*Service).dispatch()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/labrpc/labrpc.go:478 +0x84f
  labrpc.(*Server).dispatch()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/labrpc/labrpc.go:402 +0x680
  labrpc.(*Network).ProcessReq.func1()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/labrpc/labrpc.go:229 +0x93

Previous read at 0x00c0003184e0 by goroutine 101:
  encoding/gob.encOpFor.func5()
      /usr/local/go/src/reflect/value.go:1046 +0x172
  encoding/gob.(*Encoder).encodeStruct()
      /usr/local/go/src/encoding/gob/encode.go:328 +0x401
  encoding/gob.encOpFor.func4()
      /usr/local/go/src/encoding/gob/encode.go:581 +0xfa
  encoding/gob.(*Encoder).encodeArray()
      /usr/local/go/src/encoding/gob/encode.go:351 +0x224
  encoding/gob.encOpFor.func1()
      /usr/local/go/src/encoding/gob/encode.go:551 +0x1a0
  encoding/gob.(*Encoder).encodeStruct()
      /usr/local/go/src/encoding/gob/encode.go:328 +0x401
  encoding/gob.(*Encoder).encode()
      /usr/local/go/src/encoding/gob/encode.go:701 +0x246
  encoding/gob.(*Encoder).EncodeValue()
      /usr/local/go/src/encoding/gob/encoder.go:250 +0x613
  encoding/gob.(*Encoder).Encode()
      /usr/local/go/src/encoding/gob/encoder.go:175 +0x63
  labgob.(*LabEncoder).Encode()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/labgob/labgob.go:34 +0x7b
  labrpc.(*ClientEnd).Call()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/labrpc/labrpc.go:93 +0x170
  raft.(*Raft).sendAppendEntries()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/raft/raft.go:569 +0xc7
  raft.(*Raft).broadcastHeartbeat.func1()
      /Users/yy/YuanYe/Projects/github/MIT6.824-2018-Chinese/src/raft/raft.go:433 +0x47e
```
发生 race 的两个地方是

```go
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
if rf.sendAppendEntries(server, &args, &reply) // race here
```

以及

```go
if unmatch_idx != -1 {
	// there are unmatch entries
	// truncate unmatch Follower entries, and apply Leader entries
	rf.logs = rf.logs[:args.PrevLogIndex+1+unmatch_idx]
	rf.logs = append(rf.logs, args.LogEntries[unmatch_idx:]...)
}

```

即，我们在对心跳包发送的 entries 进行 encoding 的时候，同时另一个地方正在对这些 entries 进行修改。
这是完全可能发生的，因为我们的主旨是不对 RPC 调用加锁（否则无法并行），但是此时就要考虑以下情况：

- Leader 正在发送心跳包
- Leader 由于某些原因转为了 Follower，log 被新的 Leader 改写

这时，就可能出现 Leader 的 log 同时被读写的情况。为了避免这种情况，我们需要在 encoding 的时候，
将 entries 拷贝出来。而不是直接从 log 读取。

```go
prevLogIndex := rf.nextIndex[server] - 1

// use deep copy to avoid race condition
// when override log in AppendEntries()
entries := make([]LogEntry, len(rf.logs[prevLogIndex+1:]))
copy(entries, rf.logs[prevLogIndex+1:])

args := AppendEntriesArgs{
	Term:         rf.currentTerm,
	LeaderId:     rf.me,
	PrevLogIndex: prevLogIndex,
	PrevLogTerm:  rf.logs[prevLogIndex].Term,
	LogEntries:   entries,
	LeaderCommit: rf.commitIndex,
}
```

## 总结
加入 log 这一个因素后，Leader 的选举也随之复杂很多。大部分 bug 都是无法在短时间内选出 Leader 导致的。
我自己遇到的两个易错点：
- 在处理 `AppendEntries` 时，只要当前 Leader 的 term 大于等于 自身的 term，
就要重置自己的 electionTimer，因为此时还是认可 Leader 的。跟 log 无关。
- 在处理 `AppendEntries` 以及 `RequestVote` 时，首先检查 term 是否大于自身 term，如果是，
就一定立刻转为 Follower 并重置自己的 votedFor。这个也跟 log 无关。
