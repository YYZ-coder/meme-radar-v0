# H4 只读核对与分段剖析（现版收尾，真实发布待统一部署）

在backend目录执行，云库URL仅从当前进程环境RADAR_DATABASE_URL读取，不把连接串写入日志。所有云库数据查询主动使用REPEATABLE READ READ ONLY，未执行云库DDL、迁移或数据写入。云库H3/H4/H5/metadata尚未部署，两边预览对这些字段同样使用空smart集合、未知持仓/metadata；不能代替正式部署后的全字段验收。

- `go run ./testdata/expect/evidence/h4-audit`：同一快照三链×1h/4h/24h，按Token比较完整行、双币种全部20点，每语句75秒上限。before-sparse.sql为按Token生成20桶基线；sparse.sql为当前按链稀疏点+索引LATERAL种子的生产SELECT只读预览。h4-7p完成50,703行，差异0。历史h4-7l仅完成Solana1h/4h后24h超时，未当作通过。
- `go run ./testdata/expect/evidence/h4-audit/performance`：仅Solana24h，使用生产collect/fill精确搬运，报告查询解码/补桶；云库写入不执行。H1原始抽取前模板另存h1-original-template.sql。
- `go run ./testdata/expect/evidence/h4-audit/plan`：真实ANALYZE/BUFFERS计划与主键索引证据；输出h4-7n JSON，再次运行前应保留前次证据。
- `go build -o ../.local/h4-segment-profile/audit ./testdata/expect/evidence/h4-audit/profile`，然后运行该二进制：云库同一快照测EXPLAIN与原始字段接收、字节/首末行；缓存字节离线testing.Benchmark+CPU/heap pprof分开测完整行解码、Numeric codec、20桶继承。**同时必须提供RADAR_TEST_DATABASE_URL指向回环本地admin库**，工具会创建独立临时库、用正式Migrate和同批实际数据测CopyFrom/名单栅栏/退役/切head/commit后清理；拒绝非回环本地目标。当前有效证据h4-26c，前次工具修正见h4-26-harness-correction.log及交接，未伪造TDD Red。
- `go run ./testdata/expect/evidence/h4-audit/waits`：数据读取只有一个只读RR快照；另一只读连接刷新pg_stat_activity快照观察本查询的wait event，样本不是精确时间分解，也不用于对比事实值。h4-27保留原始时间样本。

`.local/h4-segment-profile`含当前匹配的二进制、三个阶段CPU/heap profile和公共原始字段缓存raw.gob，git忽略。CPU/heap已用go tool pprof -top读取，h4-26c-pprof-review.log保存分析；heap累计量含基准校准/重复，不等于峰值或单批分配。统计字节是原始字段加DataRow消息头/字段长度，不含TLS/TCP或结果描述。核对进程RSS与compute服务峰值分开报告。方案改动前后完整比较与独立四组20桶字面期望见交接及store/inflow_buckets_test.go。


服务器端发布（D-H4-7）使用从inflowPublishSelectSQL/inflowBucketSQL原样导出的array.sql。`go run ./testdata/expect/evidence/h4-audit/server-array`在同一快照比较原sparse.sql＋Go补桶与新array.sql：h4-30为50,225行、差异0；Solana24h首次/热轮接收同测。`local-array.sql`也由同一Go构造器导出，只将输入换成本地实际行表，不另写表达式。

`go run ./testdata/expect/evidence/h4-audit/local-array`需同时提供只读云库RADAR_DATABASE_URL和回环本地RADAR_TEST_DATABASE_URL：同一个云库RR只读快照原始字段在内存保留NULL/空字符串区别，随即装入隔离本地正式schema的临时输入表，再测同批三轮实际发布。不会写云库。有效证据h4-31b；先前Gob回放丢空值的测量错误留h4-31-initial和correction。旧raw.gob不能用作SQL忠实回放；profiles只用内存原始字段，CPU/分配结论不受该缓存缺陷影响。

重新导出array.sql/local-array.sql：从backend运行 `python3 testdata/expect/evidence/h4-audit/export-array.py`，直接摘取当前store的两个构造函数与列定义，不另写规则；导出前后哈希见h4-30-expression-provenance.log。服务器自然缓存首轮/热轮计划工具为 `go run ./testdata/expect/evidence/h4-audit/server-array/plan`。


最终调参E（D-H4-8）：在backend执行 `python3 testdata/expect/evidence/h4-audit/workmem/export.py`，直接摘取正式SQL/数组表达式并对云库未部署字段做同样显式mask，导出long.sql/short.sql。长窗与原array.sql哈希完全相同。`go run ./testdata/expect/evidence/h4-audit/workmem` 在一个RR只读快照中先测64MB首次/热轮、4MB随后首次/热轮的EXPLAIN ANALYZE BUFFERS，再比较三链×五窗全部37个原始二进制字段（带NULL长度）与20点；证据h4-39，50,998行差异0，64MB临时块读写0，8.168/6.387秒不是稳定7.5秒承诺。重跑会覆盖h4-39计划JSON，保留前次证据后再执行。

`go run ./testdata/expect/evidence/h4-audit/workmem/local` 同样要求只读云库和回环本地两个环境变量；只将当前实际输入装入隔离本地库，三轮发布事务均SET LOCAL 64MB，含数组表达式、新行写入、名单栅栏、退役、切head/commit。h4-40有效31,685行，完整事务1.033/2.625/2.031秒；不是云库写入时间。所有云库动作只读，结束清理的只有该工具自己新建的本地库。
