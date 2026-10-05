# query-cache-normalizer

面向查询客户端（GraphQL 风格文档）的**规范化查询缓存**。原始响应不再按整段查询结果原样
保存，而是按实体身份归并到一份归一化记录中；读取时按查询的选择集反规范化重建结果。
后端用本地内存（`MemorySource`）或文件（`FileSource`）模拟，不依赖任何外部服务。

## 核心能力

### 1. 按实体身份归并存储

- 每个对象按 `Typename:id` 归一化为一个 `Entity`，所有字段合并进同一份记录。
  默认身份字段为 `id`，可按类型配置；也可用 `DataIDFrom` 完全自定义。
- 同一实体在**不同查询、不同别名、不同变量绑定**下返回的字段都写入同一记录，不会产生多份副本。
  别名只影响响应键，不影响存储身份；变量解析后参数相同的根字段共享一个 root key。
- **字段冲突的确定性规则**：
  - 新到的“有值”字段（包括显式 `null`）覆盖旧的有值字段 —— 即**最后提交者胜**，
    保证多次更新后读取稳定地反映最新服务端真值。
  - 响应中*未出现*的字段不参与写入（合并而非覆盖），不会把已缓存的字段抹成缺失。
  - 显式 `null` 是一个真实值，缓存并按 `null` 返回，绝不与“缺失”混淆。

### 2. 从缓存重建查询结果

`Read`/`ReadQuery` 按查询选择集从归一化记录重建响应：

- 缓存完整时，重建结果与重新请求后按选择集投影的数据**逐字段一致**。
- 缓存缺少的字段以 **`MissingMarker`**（`MarkerKind() == "missing"`）显式标记，
  不会用 `null`、零值或空串冒充；`ReadResult.Complete` 同时为 `false`，
  缺失路径列在 `Missing` 中。
- 引用的实体若已被失效/回收，引用处也是一个可区分的 `MissingMarker`（悬空引用）。

### 3. 多种查询形态

- **字段选择不同的同一实体**：读回时只投影该查询选择的字段，互不串扰。
- **嵌套引用**：被识别对象存为 `Ref`，读取时解析；无身份对象内嵌存储。
- **列表**：逐元素归一化并保留顺序。
- **联合/接口类型**：以内联片段的类型条件（`Selection.TypeName`）配合运行时
  `__typename` 过滤，`Post` 的字段不会写进或读成 `Comment`，反之亦然。

### 4. 并发读写与快照隔离

- 状态是不可变发布单元。写操作在当前状态的**写时复制（copy-on-write）克隆**上修改，
  完成后通过原子指针一次性发布，并用单调递增的 `seq` 标记版本。
- 读操作无锁遍历某个已发布快照，因此**任何时刻读到的都是某个已提交的一致快照**；
  写入进行中读方绝不可能看到半更新的实体或引用。
- 已通过 `go test -race` 下的多写多读压力测试。

### 5. 失效与垃圾回收

- `InvalidateEntity(id)`：删除实体，并清理所有指向它的字段 / 列表元素 / 内嵌键；
  返回受影响的查询指纹（`AffectedQueries`）。
- `InvalidateField(id, field)`：定向清除单个归一化字段（或 `ROOT_QUERY.<rootField>`）。
- `GC()`：以“已注册查询的根字段”为根做可达性遍历，回收不再被任何查询引用的实体
  与根字段。
- 失效后相关查询再读，其结果与“清空后重新请求”一致：被清除处是可区分的
  `MissingMarker`，其余字段保持不变。

### 6. 部分失败与错误响应

- `Data == nil` 且带错误 = 顶层失败：**不提交任何新快照**，已有缓存完全不受污染。
- `Data` 与字段级错误并存：健康字段正常归一化，错误路径所在字段记为
  `FieldSkipped`，读取时返回 **`SkippedMarker`**（`MarkerKind() == "skipped"`，
  携带原始 `FieldError`），与真正缺失的 `MissingMarker` 明确区分。
- **跳过绝不覆盖有值**：部分失败不会抹掉此前的好数据；后续健康响应会修复跳过位。
- 列表元素的错误用索引路径（`["users", 1, "name"]`）定位。

### 7. 持久化与恢复

- `Save/SaveTo` 以**确定性 JSON**（对象键排序、整数不失真）序列化；
  `Load/LoadFrom` 恢复。归一化关系（实体、`Ref`、跳过标记、根字段、查询注册）
  完整往返，恢复后重建结果与恢复前逐条一致。
- 相同操作序列 + 固定配置产生**字节级一致**的可观测输出（见
  `TestOperationSequenceDeterministic`、`TestPersistDeterministic`）。

## 配置项 `Config`

| 字段 | 说明 |
| --- | --- |
| `IDFields` | `typename -> 身份字段列表`；`""` 键为全局默认，默认 `["id"]`。多字段身份会确定性地组合成键。 |
| `DataIDFrom` | 自定义身份推导；返回 `("", false)` 表示该对象不单独归一化而内嵌存储。 |
| `PersistPath` | 非空时 `Save()/Load()` 使用的文件路径。 |

## 快速开始

```go
c := qcache.New(qcache.Config{})

q := &qcache.Query{
    Name: "Profile", Type: qcache.OpQuery,
    Variables: map[string]any{"uid": int64(1)},
    Selections: []qcache.Selection{{
        Name: "user",
        Args: map[string]any{"id": qcache.Variable{Name: "uid"}},
        Selections: []qcache.Selection{{Name: "id"}, {Name: "name"}},
    }},
}

_, _ = c.Write(q, &qcache.Response{Data: map[string]any{
    "user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
}})

res, _ := c.ReadQuery(q)
fmt.Println(res.Data) // map[user:map[id:1 name:Ada]]
```

用模拟数据源：

```go
src := qcache.NewMemorySource()
src.Set(q, resp)
resp, wr, err := c.Fetch(ctx, src, q) // 执行 + 归一化写入
```

## 存储模型

- `state.entities["ROOT_QUERY"]` 保存根字段，键形如 `user({"id":1})`（参数是身份的一部分）。
- 每个实体键 `Typename:id`，其 `Fields` 为字段名到 `Field{Status, Value, Err}`。
- 有身份对象的值是 `Ref{"$ref": "User:1"}`；内嵌对象是普通 map；
  内嵌对象中的跳过位用内部哨兵表示，读取时转换为公开的 `SkippedMarker`。

## 测试

```bash
go vet ./...
go test -race ./...
```

覆盖：字段合并与冲突、查询重建与缺失标记、嵌套/列表/联合不串扰、并发快照隔离、
实体/字段定向失效与 GC、部分失败（顶层失败/字段跳过/修复）、持久化往返与确定性。
