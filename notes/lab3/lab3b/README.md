# Lab 3 Part B: Key/value service with log compaction

现在我们已经完成了 key-value 存储服务的基本功能，但是这样的实现无法长期运行。
因为 Raft 的 log 会无限增长，导致内存不足。我们需要修改 Raft 和 kvserver 来节省空间。

kvserver 会时不时存储一个当前状态的快照，Raft 会丢弃在快照之前的所有 log。
当 server 重启，它首先载入这个快照，然后会将在快照之后的 Raft log 全部执行一遍。

具体流程在论文的 section 7 中, 同时参考[Guide: An aside on optimizations](https://thesquareplanet.com/blog/students-guide-to-raft/#an-aside-on-optimizations)。

## 大概思路

这个 lab 中心思想非常简单，就是用 kvserver 的 snapshot 来替换一些太老的 log。
这样就能减少内存的占用，并且在 server 重启后不用花太长时间去恢复。

大概需要做以下几件事：
1. 在 kvserver 中检测 raft log 的大小，如果超出限制则发起 snapshot，encode 自己的状态。
2. kvserver 将 encode 后的状态提供给 raft 的 leader，附带最后一个执行的 log 的 index
3. leader 将 index 之前的所有 log 都截掉，因为他们与 snapshot 中的状态是等效的
4. leader 向所有 follower 发起 snapshot 的同步请求 `sendInstallSnapshot`
5. follower 执行 `InstallSnapshot`，使自身的 snapshot 与 log 都和 leader 一致，
  然后通过 `applyCh` 将 leader 的 snapshot 发送给自身的 kvserver。
6. kvserver 收到 raft 发送的 snapshot 后更新自身状态，达到同步。

## 难点

lab 3B 给人的感觉就是初看似乎不难，实际我觉得应该是最难的一个。

因为它基本完全破坏了之前 raft 的结构。整个做下来感觉没有一点美感。完全是 debug 再 debug。

遇到了不少问题，现在把个人认为的难点整理如下。

### 两种 index

在部分 log 被替换成 snapshot 后，log 的 index 已经不能作为真正的 index，
而是“相对的”的 index，”绝对的“ index 需要记录从 raft 开始运行至现在的 index。
因此我们需要频繁在这两种 index 之间切换。例如需要利用 index 找到对应的 log，
则应该使用相对的 index，反之需要使用绝对的 index。

因此我们引入以下两个工具函数：
```go
func (rf *Raft) getRelativeLogIndex(index int) int {
	// index of rf.logs
	return index - rf.snapshottedIndex
}

func (rf *Raft) getAbsoluteLogIndex(index int) int {
	// index of log including snapshotted ones
	return index + rf.snapshottedIndex
}
```

### 耗时太久

之前一直没有发现自己实现上的问题，直到做到 lab 3B，才暴露出来。之前的实现里，在`Raft.Start()`
中没有去直接发起同步，而是被动等待下一个心跳包。这样导致在 Raft 上搭建应用后，对于每一个 client，
一个操作的返回需要的时间至少是一个心跳包（这样才能完成 log 同步）。

修复这个 sb 问题的方法也很简单，增加一次发起心跳包在 `Raft.Start()` 中即可。
```go
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	term, isLeader = rf.GetState()
	if isLeader {
		rf.mu.Lock()
		index = rf.getAbsoluteLogIndex(len(rf.logs))
		rf.logs = append(rf.logs, LogEntry{Command: command, Term: term})
		rf.matchIndex[rf.me] = index
		rf.nextIndex[rf.me] = index + 1
		rf.persist()
		rf.mu.Unlock()
		// start agreement now
		go func() {
			rf.mu.Lock()
			defer rf.mu.Unlock()
			rf.broadcastHeartbeat()
		}()
	}

	return index, term, isLeader
}
```

### 卡死在 `Test: many clients (3A) ...`

在修复耗时过长的问题后，立刻遇到了这个问题。经检查 log，发现 Leader 总是丢失一些需要 apply 的操作。
这个问题在单个 client 时候不出现，多个 client 时必定出现。
而且是在 `Start()` 中增加了调用 `broadcastHeartbeat()` 后出现。
所以几乎立刻就知道肯定是某个地方并发没处理好。

```go
ch, ok := kv.dispatcher[msg.CommandIndex]
kv.mu.Unlock()

if ok {
        notify := Notification{
                ClientId:  op.ClientId,
                RequestId: op.RequestId,
        }
        ch <- notify
}
```
注意以上这一段代码，假设以下情形出现：
1. 在线程 A 中 `dispatcher` 中找到了 `msg.CommandIndex` 对应的通知 channel 并赋值给 `ch`
2. 在另一个线程 B 中从 `dispatcher` 处理并删除了 `msg.CommandIndex` 对应的通知 channel
3. 在线程 A 中 `ch` 仍持有channel，并向其中发送通知
4. 由于已经没有线程从其中获取通知，所以卡死。

发现有问题的 log 如下：
```
2019/09/08 21:25:10.480022 [node(0), state(Leader), term(1), snapshottedIndex(0)] apply from index 46 to 59
...
2019/09/08 21:25:10.480727 [node(0), state(Leader), term(1), snapshottedIndex(0)] apply from index 56 to 60
...
2019/09/08 21:25:17.544046 [node(0), state(Leader), term(1), snapshottedIndex(0)] apply from index 56 to 127
```
由于每次更改 `commitIndex` 都会 kickoff 一个 goroutine 来 apply，所以不同的 goroutine 可能重复 apply 同一个 log，
在第一次 apply 结束后，按照现有逻辑，会把通知 client 用的 chan 从 `dispatcher` 中删除，导致此后其他的 apply 卡死。

解决方式是，我们只通知不重复的结果。对于那些由于被过滤掉无法收到结果的，在超时后检查其是否已经被执行，
如果已经被执行则返回 `wrongLeader = false`，说明我们认为无需再次发起这个请求。

从 `applyCh` 获取 Command，执行后再通知等待的 client：
```go
// You may need initialization code here.
go func() {
  for msg := range kv.applyCh {
    if msg.CommandValid == false {
      switch msg.Command.(string) {
      case "InstallSnapshot":
        kv.installSnapshot(msg.CommandData)
      }
      continue
    }

    op := msg.Command.(Op)
    kv.mu.Lock()
    if kv.isDuplicateRequest(op.ClientId, op.RequestId) {
      kv.mu.Unlock()
      continue
    }
    switch op.Name {
    case "Put":
      kv.db[op.Key] = op.Value
    case "Append":
      kv.db[op.Key] += op.Value
      // Get() does not need to modify db, skip
    }
    kv.lastAppliedRequestId[op.ClientId] = op.RequestId
    kv.appliedRaftLogIndex = msg.CommandIndex

    if ch, ok := kv.dispatcher[msg.CommandIndex]; ok {
      notify := Notification{
        ClientId:  op.ClientId,
        RequestId: op.RequestId,
      }
      ch <- notify
    }

    kv.mu.Unlock()
    DPrintf("kvserver %d applied command %s at index %d, request id %d, client id %d",
            kv.me, op.Name, msg.CommandIndex, op.RequestId, op.ClientId)
  }
}()
```

client 处理收到的通知：
```go
select {
case notify := <-ch:
  if notify.ClientId != op.ClientId || notify.RequestId != op.RequestId {
    // leader has changed
    wrongLeader = true
  } else {
    wrongLeader = false
  }

case <-time.After(timeout):
  kv.mu.Lock()
  if kv.isDuplicateRequest(op.ClientId, op.RequestId) {
    wrongLeader = false
  } else {
    wrongLeader = true
  }
  kv.mu.Unlock()
}
```

### 各种 index out of range
由于增加了 snapshot 机制，特别容易出现 log index 的问题。现列举我遇到的如下。


#### 在处理 AppendEntries 时
```
2019/09/15 21:23:04.991322 [node(4), state(Leader), term(2), snapshottedIndex(13987)] sync snapshot with server 0 for index 13987, last snapshotted = 13987
2019/09/15 21:23:04.991328 [node(4), state(Leader), term(2), snapshottedIndex(13987)] sync snapshot with server 2 for index 13987, last snapshotted = 13987
2019/09/15 21:23:04.991374 [node(1), state(Follower), term(2), snapshottedIndex(13971)] sync log with leader 4, prevLogIndex=13987, log length=17
2019/09/15 21:23:04.991382 [node(1), state(Follower), term(2), snapshottedIndex(13971)] apply from index 13987 to 13987
2019/09/15 21:23:04.991462 [node(2), state(Follower), term(2), snapshottedIndex(13987)] sync log with leader 4, prevLogIndex=13987, log length=1
2019/09/15 21:23:04.991475 [node(0), state(Follower), term(2), snapshottedIndex(13987)] sync log with leader 4, prevLogIndex=13986, log length=1
2019/09/15 21:23:04.991497 [node(2), state(Follower), term(2), snapshottedIndex(13987)] install snapshot from 4
panic: runtime error: index out of range
```

从log 中可以看出，这是由于 `args.PrevLogIndex` 已经被 snapshot 替换，导致越界。
```go
	if rf.logs[rf.getRelativeLogIndex(args.PrevLogIndex)].Term != args.PrevLogTerm {
    ...
  }
```

解决办法是在之前加入处理这种情况的逻辑。
```go
if args.PrevLogIndex <= rf.snapshottedIndex {
  reply.Success = true

  // sync log if needed
  if args.PrevLogIndex + len(args.LogEntries) > rf.snapshottedIndex {
    // if snapshottedIndex == prevLogIndex, all log entries should be added.
    startIdx := rf.snapshottedIndex - args.PrevLogIndex
    // only keep the last snapshotted one
    rf.logs = rf.logs[:1]
    rf.logs = append(rf.logs, args.LogEntries[startIdx:]...)
  }

  return
}
```

#### 在处理 AppendEntries 的返回值时
```
2019/09/14 12:01:59.910493 partition servers into: [0 2] [1]
...
2019/09/14 12:02:00.361369 partition servers into: [0 1 2] []
2019/09/14 12:02:00.361590 [node(1), state(Leader), term(1), snapshottedIndex(38)] sync snapshot with server 2 for index 38, last snapshotted = 38
2019/09/14 12:02:00.361757 Term 6: server 1 convert from Leader to Follower
2019/09/14 12:02:00.361882 [node(1), state(Follower), term(6), snapshottedIndex(38)] conflict with server 0, prevLogIndex 53, log length = 17
panic: runtime error: index out of range
```

在有两个 partition 时，0 和 1 号 server 都认为自己是 Leader，在合并后，1 变为 Follower，
但是在此之前它发送了 `AppendEntries`，在处理时发现与 0 冲突，在下面逻辑中报错：

```go
for i := args.PrevLogIndex; i >= 1; i-- {
  if rf.logs[rf.getRelativeLogIndex(i-1)].Term == reply.ConflictTerm {
    // in next trial, check if log entries in ConflictTerm matches
    rf.nextIndex[server] = i
    break
  }
}
```
解决方法是，在处理 `AppendEntries` 的 reply 时，首先判断自己还是不是 Leader，如果不是直接返回。
```go
if rf.sendAppendEntries(server, &args, &reply) {
  rf.mu.Lock()
  if rf.state != Leader {
    rf.mu.Unlock()
    return
  }
  ...
}
```

#### 在 apply 达成一致的 log entry 时

```
2019/09/15 12:38:11.935965 [node(2), state(Follower), term(1), snapshottedIndex(0)] apply from index 21 to 28
2019/09/15 12:38:11.936129 [node(3), state(Follower), term(1), snapshottedIndex(0)] apply from index 20 to 28
2019/09/15 12:38:11.936201 [node(4), state(Follower), term(1), snapshottedIndex(0)] apply from index 16 to 28
2019/09/15 12:38:11.936567 [node(0), state(Follower), term(1), snapshottedIndex(0)] apply from index 26 to 28
2019/09/15 12:38:11.936895 [node(1), state(Leader), term(1), snapshottedIndex(16)] apply from index 15 to 30
panic: runtime error: slice bounds out of range
```

```go
go func(startIdx int, entries []LogEntry) {
  for idx, entry := range entries {
    var msg ApplyMsg
    msg.CommandValid = true
    msg.Command = entry.Command
    msg.CommandIndex = startIdx + idx
    rf.applyCh <- msg
    // do not forget to update lastApplied index
    // this is another goroutine, so protect it with lock
    rf.mu.Lock()
    rf.lastApplied = msg.CommandIndex
    rf.mu.Unlock()
  }
}(rf.lastApplied+1, entriesToApply)
```

在以上的实现中，我们并未考虑并行执行时有可能出现的乱序问题，比如，
goroutine A 正在 apply 15~30，而在此之前 goroutine B 已经执行完了 16 并 snapshot。
因此我们需要保证 `rf.lastApplied` 单调递增，否则执行慢的 goroutine 会覆盖执行快的 `lastApplied`，
出现越界。修改如下：

```go
go func(startIdx int, entries []LogEntry) {
  for idx, entry := range entries {
    var msg ApplyMsg
    msg.CommandValid = true
    msg.Command = entry.Command
    msg.CommandIndex = startIdx + idx
    rf.applyCh <- msg
    // do not forget to update lastApplied index
    // this is another goroutine, so protect it with lock
    rf.mu.Lock()
    if rf.lastApplied < msg.CommandIndex {
      rf.lastApplied = msg.CommandIndex
    }
    rf.mu.Unlock()
  }
}(rf.lastApplied+1, entriesToApply)
```

#### 在 InstallSnapshot 时

这里主要是没考虑 `args.LastIncludedIndex < rf.snapshottedIndex` 的情况。
在多个 client 并发处理时，这个情况也是可能的。其原因是以下代码可能由多个 goroutine 同时运行：

```go
if kv.shouldTakeSnapshot() {
  kv.takeSnapshot()
}
```
因此我们现在需要处理这种特殊情况，在 `raft.InstallSnapshot` 中加入：

```go
reply.Term = rf.currentTerm
if args.Term < rf.currentTerm || args.LastIncludedIndex < rf.snapshottedIndex {
  return
}
```

### linearizability 测试 fail

这个我觉得应该是所有人都会遇到的问题，卡了我很久。其根本原因是在 `InstallSnapshot` 的处理中，
如果这个 follower 的 `appliedIndex` 已经大于同步的 snapshot 对应的 index，
那么我们就不应该用旧的 snapshot 去覆盖 kvserver 的状态。否则在之后如果再

```go
if rf.lastApplied > rf.snapshottedIndex {
  // snapshot is elder than kv's db
  // if we install snapshot on kvserver, linearizability will break
  return
}

installSnapshotCommand := ApplyMsg{
  CommandIndex: rf.snapshottedIndex,
  Command:      "InstallSnapshot",
  CommandValid: false,
  CommandData:  rf.persister.ReadSnapshot(),
}
go func(msg ApplyMsg) {
  rf.applyCh <- msg
}(installSnapshotCommand)
```

## 参考
有一个写的比较详细的博客，我觉得他总结的问题应该也是大家都会遇到的，可以和我的互相补充。
- [基于RAFT 实现容错KV服务](https://www.jianshu.com/p/8fc46f12a106)
