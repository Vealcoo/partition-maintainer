# Partition Flow

## Overview

這個專案的工作是定期維護 MySQL `RANGE` partitions。

核心假設：

- 資料表已經是 partition table
- partition expression 必須是 `TO_DAYS(PARTITION_COLUMN)`
- partition 名稱格式是 `pYYYYMMDD`
- 最後一個 partition 必須是 `pmax`

主流程入口在 [`cmd/partition-maintainer/main.go`](./cmd/partition-maintainer/main.go)。
核心邏輯在 [`internal/partition/manager.go`](./internal/partition/manager.go)。

## Runtime Flow

每次 job 執行時，流程如下：

1. 讀取環境變數並組成 `config.Config`
2. 載入 `TIMEZONE`，把 `time.Now()` 對齊到該時區的本地午夜
3. 連線 MySQL 並 `Ping`
4. 若設定了 `PARTITION_LOCK_NAME`，先取得 MySQL `GET_LOCK`
5. 驗證目標表是否符合 partition 規格
6. 必要時修補尾端缺口
7. 建立未來 partitions
8. 刪除過期 partitions
9. 釋放 lock

## Validation Rules

程式在真正做 DDL 前會先驗證：

- `TABLE_NAME` 只能是英數與底線
- partition method 必須是 `RANGE`
- partition expression 必須正好是 `TO_DAYS(PARTITION_COLUMN)`
- 最少要有一個資料 partition 加上 `pmax`
- `pmax` 必須在最後，且 `PARTITION_DESCRIPTION` 必須是 `MAXVALUE`
- 每個資料 partition 名稱都必須可解析為 `pYYYYMMDD`
- 每個資料 partition 的 boundary 必須等於 `partition start + PARTITION_SPAN_DAYS`
- 相鄰資料 partitions 必須剛好相差 `PARTITION_SPAN_DAYS`

只要上面任何一項不成立，job 會直接 fail，不會繼續維護。

## Partition Span

`PARTITION_SPAN_DAYS` 代表每個 partition 覆蓋幾天。

- `1`：每天一個 partition
- `7`：每 7 天一個 partition
- `13`：每 13 天一個 partition

bucket 對齊不是看月份或星期，而是用 MySQL `TO_DAYS(date)` 做固定切分。
因此跨月、跨年都可以正常驗證；但既有 partitions 必須和設定的 span 一致，不能把已按日建立的表直接切成 `13` 天模式繼續跑。

## Forward Gap Repair

如果已有資料 partitions 本身是合法且連續的，但最後一個資料 partition 落後太多，程式可選擇自動補尾端缺口。

條件：

- `AUTO_REPAIR_FORWARD_GAPS=true`
- 缺口只發生在「最後一個資料 partition」之後
- 缺口位於目前維護視窗內
- active window 不可早於最早既有資料 partition
- 缺口數量不超過 `MAX_REPAIR_PARTITIONS_PER_RUN`

修補方式：

- 使用 `ALTER TABLE ... REORGANIZE PARTITION pmax INTO (...)`
- 一次補齊所有缺少的 forward buckets
- 修補後重新查詢 `INFORMATION_SCHEMA.PARTITIONS`
- 驗證新 partitions 是否真的存在

如果 `AUTO_REPAIR_FORWARD_GAPS=false`，遇到這種尾端缺口會直接 fail。

## Create Flow

`CREATE_AHEAD_DAYS` 控制的是「維護視窗往未來覆蓋幾天」。

程式會：

1. 找出 `today` 所在 bucket
2. 找出 `today + CREATE_AHEAD_DAYS - 1` 所在 bucket
3. 列出這個區間內所有應存在的 bucket
4. 把缺少的 buckets 透過 `REORGANIZE pmax` 建出來

例如：

- `PARTITION_SPAN_DAYS=1`
- `CREATE_AHEAD_DAYS=30`
- `today=2026-04-15`

則維護目標會覆蓋 `p20260415` 到 `p20260514`。

## Drop Flow

`DROP_BEFORE_DAYS` 控制的是保留資料的天數。

程式會先計算：

- `cutoff = today - DROP_BEFORE_DAYS`

之後逐個檢查資料 partitions：

- 如果 partition 的結束日 `<= cutoff`，就列入刪除
- 如果 partition 還有任一天落在保留期內，就不刪

所以在 `PARTITION_SPAN_DAYS=1` 時，行為就是「刪掉 90 天前的日 partition」；
在 `PARTITION_SPAN_DAYS>1` 時，則是「整個 bucket 都過期才刪」。

保護條件：

- 單次刪除數量不能超過 `MAX_DROP_PARTITIONS_PER_RUN`
- 不允許一次刪掉所有非 `pmax` partitions

## Dry Run

`DRY_RUN=true` 時：

- repair 只印 repair SQL
- create 只印 create SQL
- drop 只印 drop SQL

不會真的執行 DDL。

這適合第一次接表、調整設定、或驗證 retention 規則時使用。

## .env.local Example

目前專案內的 [`.env.local`](./.env.local) 是：

- `TABLE_NAME=partition_test`
- `PARTITION_COLUMN=created_at`
- `TIMEZONE=Asia/Taipei`
- `PARTITION_SPAN_DAYS=1`
- `CREATE_AHEAD_DAYS=30`
- `DROP_BEFORE_DAYS=90`
- `MAX_CREATE_PARTITIONS_PER_RUN=30`
- `MAX_DROP_PARTITIONS_PER_RUN=30`
- `MAX_REPAIR_PARTITIONS_PER_RUN=50`
- `AUTO_REPAIR_FORWARD_GAPS=true`
- `DRY_RUN=false`

這組設定的效果是：

- 每天一個 partition
- 以台北時區計算今天
- 維護未來 30 天的 partitions
- 保留最近 90 天資料
- 允許自動修補安全的尾端缺口
- 目前會真的執行 DDL，不是 dry-run

如果今天是 `2026-04-15`，則：

- create window 會覆蓋 `p20260415` 到 `p20260514`
- drop cutoff 會是 `2026-01-15`
- `p20260114` 以及更早的日 partition 可被刪除

## Recommended Operation

建議操作順序：

1. 先用 `DRY_RUN=true` 跑一次
2. 確認 partition expression、命名、現有 layout 都符合規則
3. 若表有尾端缺口，視需要開 `AUTO_REPAIR_FORWARD_GAPS=true`
4. 確認輸出的 repair/create/drop SQL 符合預期
5. 再把 `DRY_RUN=false` 正式執行

## Important Caveat

這個專案目前只適合維護「已經按規則建立好的 partition table」。

它不負責：

- 自動把普通 table 轉成 partition table
- 自動重切已存在但規則不一致的 partitions
- 自動修正歷史中間缺口
- 自動回填比最後既有資料 partition 更早的缺口

尾端缺口 repair 是有界自動化，不是全自動 schema repair。
