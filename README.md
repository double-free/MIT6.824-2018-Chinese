# MIT6.824-2018-Chinese
A Chinese version of MIT 6.824 (distributed system)

See [MIT6.824-2018](https://pdos.csail.mit.edu/6.824/schedule.html) for details.

## 介绍
之前的项目([MIT6.284-2017-Chinese](https://github.com/double-free/MIT6.824-2017-Chinese))由于个人原因（懒狗一条，外加毕业和工作的事）虎头蛇尾地扔在了一边。最近突然翻出来一看，觉得自己写的真的很shi。现在重开一坑，直接从 lab2 开始写吧。这次保证要保质保量完成咕咕。

还是先总结下之前的失败点：

- 较多的 race condition 没有妥善处理
- 代码不够优美，主循环的 select 中，忙询 raft 状态，并在 Leader 的处理逻辑中 sleep。

避免 race condition 是非常关键的要求，因为如果没有通过 `go test -race` 测试的代码有几率导致某些 test case 失败。而这要求较好的 raft 结构设计。

## 进度
- [x] **Lab1: mapreduce** 请移步[上一个项目](https://github.com/double-free/MIT6.824-2017-Chinese)
- [x] **Lab2: Raft**
  - [Part A notes](https://github.com/double-free/MIT6.824-2018-Chinese/tree/master/notes/lab2/lab2a)
  - [Part B notes](https://github.com/double-free/MIT6.824-2018-Chinese/tree/master/notes/lab2/lab2b)
  - [Part C notes](https://github.com/double-free/MIT6.824-2018-Chinese/tree/master/notes/lab2/lab2c)
- [ ] **Lab3: KV Raft**
  - [Part A notes](https://github.com/double-free/MIT6.824-2018-Chinese/tree/master/notes/lab3/lab3a)
