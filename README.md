# partition-maintainer

用 Cloud Run Job / Cloud Scheduler 定時維護 MySQL range partitions 的 Go 專案範例。

## 功能
- 預先建立覆蓋未來 N 天的 partition
- 刪除保留天數以前、且整個 partition 已完全過期的 partition
- 使用 `pmax` + `REORGANIZE PARTITION`
- 執行前驗證目標表為 `RANGE` partition，並限制單次 DDL 變更數量
- 支援 dry-run
- 支援 MySQL `GET_LOCK` 防重入
- 適合部署到 GCP Cloud Run Job

## 假設
- 目標資料表已經是 partition table
- partition 名稱格式為 `pYYYYMMDD`，代表該 partition 的起始日
- table 上存在 `pmax`
- partition key 使用 `TO_DAYS(date_column)`

## 環境變數

### 必填
- `INSTANCE_CONNECTION_NAME`
- `DB_USER`
- `DB_NAME`
- `TABLE_NAME`

### 密碼來源
- `DB_PASSWORD` 或 `DB_PASSWORD_SECRET` 擇一
- `DB_PASSWORD_SECRET` 可用完整 Secret Manager resource name，例如 `projects/my-project/secrets/db-password/versions/latest`
- 若 `DB_PASSWORD_SECRET` 只填 secret id，例如 `db-password`，則必須另外提供 `GCP_PROJECT_ID`
- 不允許同時設定 `DB_PASSWORD` 與 `DB_PASSWORD_SECRET`

### 選填
- `GCP_PROJECT_ID` 僅在 `DB_PASSWORD_SECRET` 不是完整 resource name 時需要
- `TIMEZONE` 預設 `Asia/Taipei`
- `PARTITION_SPAN_DAYS` 預設 `1`
- `CREATE_AHEAD_DAYS` 預設 `30`
- `DROP_BEFORE_DAYS` 預設 `90`
- `MAX_CREATE_PARTITIONS_PER_RUN` 預設 `30`
- `MAX_DROP_PARTITIONS_PER_RUN` 預設 `30`
- `MAX_REPAIR_PARTITIONS_PER_RUN` 預設 `30`
- `AUTO_REPAIR_FORWARD_GAPS` 預設 `false`
- `DB_PORT` 預設 `3306`
- `PARTITION_LOCK_NAME` 預設 `partition-maintainer:<table>`
- `LOCK_TIMEOUT_SECONDS` 預設 `10`
- `DRY_RUN` 預設 `false`

## 本地測試
```bash
go mod tidy
go build ./cmd/partition-maintainer
```

## Docker build
```bash
docker build -t partition-maintainer:latest .
```

## Cloud Run Job
建議使用 `DB_PASSWORD_SECRET`，並授權 Cloud Run Job 的 service account `Secret Manager Secret Accessor`。

## DDL 風險控制
- 執行前會檢查目標表存在 partition，且 partition method 必須是 `RANGE`
- 執行前會檢查 partition expression 必須是 `TO_DAYS(PARTITION_COLUMN)`
- 執行前會檢查 partition 連續性、命名與 `PARTITION_SPAN_DAYS` 邊界是否一致
- 若尾端缺口只發生在最後一個資料 partition 與目前維護視窗之間，可用 `AUTO_REPAIR_FORWARD_GAPS=true` 自動補齊
- 單次最多建立 `MAX_CREATE_PARTITIONS_PER_RUN` 個 partition
- 單次最多刪除 `MAX_DROP_PARTITIONS_PER_RUN` 個 partition
- 單次最多修補 `MAX_REPAIR_PARTITIONS_PER_RUN` 個尾端缺口 partition
- 若本次刪除會把所有非 `pmax` partition 全部清空，程式會拒絕執行
- DDL 完成後會重新查詢 `INFORMATION_SCHEMA.PARTITIONS`，確認建立或刪除結果符合預期

## Partition Span
- `PARTITION_SPAN_DAYS=1` 時，行為與原本相同，代表每天一個 partition
- `PARTITION_SPAN_DAYS=7` 時，每個 partition 覆蓋 7 天，名稱是該 bucket 的起始日
- 例如 `2026-04-15` 所在的 7 天 bucket，名稱可能是 `p20260411`，邊界到 `TO_DAYS('2026-04-18')`
- bucket 對齊方式以 MySQL `TO_DAYS(date)` 為基準做固定切分，因此不會因為程式重跑而漂移

## 範例 SQL
```sql
CREATE TABLE partition_test (
    id BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (id, created_at),
    KEY idx_created_at (created_at)
)
PARTITION BY RANGE (TO_DAYS(created_at)) (
    PARTITION p20260411 VALUES LESS THAN (TO_DAYS('2026-04-12')),
    PARTITION pmax VALUES LESS THAN MAXVALUE
);
```
