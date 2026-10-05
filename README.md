# query-cache-normalizer-go

面向查询客户端的**规范化缓存**。与传统“整段响应原样保存”的响应缓存不同，
本库把响应按**实体身份**拆分为记录存储：同一实体无论在哪条查询、哪个别名、
哪组变量下返回，其字段都合并进同一份记录，彻底消除多份陈旧副本。

查询与响应输入均由本地内存/文件模拟（`ParseQuery` + `map[string]any` 数据），
不依赖任何外部服务。

## 快速上手

```go
c := normcache.New()

q, _ := normcache.ParseQuery(`query GetUser($id: ID!) {
    user(id: $id) { id name friends { id name } }
}`)

// 写入：响应按实体归一化，嵌套对象存为引用
c.Write(q, map[string]any{"id": "1"}, map[string]any{
    "user": map[string]any{"__typename": "User", "id": "1", "name": "Ann",
        "friends": []any{map[string]any{"__typename": "User", "id": "2", "name": "Ben"}}},
}, nil)

// 读取：从缓存重建查询结果
res := c.Read(q, map[string]any{"id": "1"})
res.Complete // true：所有字段命中
```

## 一致性语义

### 归一化与合并

- 实体身份 = `__typename` + `id`（字段名可配置），记录键形如 `User:1`。
- 同一实体在不同查询、别名、变量下返回的字段**合并进同一份记录**；
  重复返回不产生副本。更新一处，所有引用该实体的查询读取立即可见。
- **字段冲突规则：后写胜出（last write wins）。** 对相同的操作序列，
  结果完全确定，可重放。
- 字段存储键 = 字段名 + 规范化后的参数（参数键排序、变量已解析），
  因此 `user(id: $a)` 与 `user(id: $b)` 在变量值相同时命中同一字段，
  不同参数值（如 `friends(first: 2)` 与 `friends(first: 10)`）互不覆盖。

### 读取重建

- `Read` 从缓存重建与重新请求结构一致的结果。
- 缓存缺少的字段以 `Missing{Field}` 标记放入结果，**绝不用 nil/空值冒充**；
  此时 `ReadResult.Complete == false`。
- 上次响应带错误被跳过的字段以 `Errored{Field, Message}` 标记，
  与真正缺失（`Missing`）严格区分。

### 并发

- 每次 `Write` 在单把写锁内完成，是一次原子提交；
  `Read` 持读锁，任一时刻读到的都是**某个已提交写入的完整快照**，
  不会看到部分更新的实体或引用。
- 并发读写经 `go test -race` 验证；测试同时校验不会读到
  “name 来自第 i 次写入、age 来自第 j 次写入”的撕裂状态。

### 失效与垃圾回收

- `InvalidateEntity(key)`：删除整个实体记录，之后引用它的读取得到 `Missing`。
- `InvalidateField(key, fieldKey)`：定向失效单个字段（含参数化字段键，
  可用 `FieldKey` 生成），其余字段不受影响。
- `GC()`：mark-sweep 回收不再被根查询传递引用的实体记录，
  返回被回收的键（排序，确定性）。失效后相关查询的读取结果与重新请求一致
  （缺失字段以 `Missing` 呈现，`Complete == false`）。

### 部分失败与错误响应

- `Write` 接受 `[]ResponseError`（含路径与消息）。路径命中的字段**不写入**，
  在记录中留下 `Errored` 标记——带错误的响应不会污染已有缓存值，
  被跳过的字段与真正缺失的字段可区分，后续读取如实反映该状态。
- 响应中未出现的字段（无错误）不触碰已有缓存值。

### 持久化与恢复

- `Snapshot(w)` 将缓存确定性地序列化（记录按键排序、字段值带类型判别），
  相同缓存状态产生逐字节相同的输出。
- `Restore(r)` 完整恢复记录、字段值（含 `Errored` 标记）与归一化引用关系，
  以及配置项。恢复后相同操作序列的可观测结果与恢复前逐条一致。

## 配置项

`NewWithOptions(Options{...})`：

| 字段 | 默认值 | 说明 |
|---|---|---|
| `IDField` | `"id"` | 实体身份字段名 |
| `TypenameField` | `"__typename"` | 实体类型字段名（同时用于联合类型内联片段匹配） |

零值 `Options{}` 即默认配置（`normcache.New()`）。

## 查询语法

支持 GraphQL 子集：字段、别名（`me: user`）、参数（含变量 `$id`、
字符串/数字/布尔/null/列表/对象字面量）、嵌套选择、
内联片段（`... on User { ... }`，用于联合/接口类型）、
可选操作头（`query Name($id: ID!)`）。列表形态由响应数据决定。

## 测试

```sh
go test ./...        # 单元测试
go test -race ./...  # 并发竞态检测
```

覆盖：字段合并、别名/变量归并、冲突规则、查询重建、缺失/错误标记、
嵌套/列表/联合类型、参数化字段、并发读写、定向失效、GC、
部分失败、持久化往返与确定性。
