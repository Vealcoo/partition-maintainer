# partition-maintainer

用 Cloud Run Job / Cloud Scheduler 定時維護 MySQL range partitions 的 Go 專案範例。

## 功能
- 預先建立未來 N 天 partition
- 刪除保留天數以前的 partition
- 使用 `pmax` + `REORGANIZE PARTITION`
- 執行前驗證目標表為 `RANGE` partition，並限制單次 DDL 變更數量
- 支援 dry-run
- 支援 MySQL `GET_LOCK` 防重入
- 適合部署到 GCP Cloud Run Job

## 假設
- 目標資料表已經是 partition table
- partition 名稱格式為 `pYYYYMMDD`
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
- `CREATE_AHEAD_DAYS` 預設 `30`
- `DROP_BEFORE_DAYS` 預設 `90`
- `MAX_CREATE_PARTITIONS_PER_RUN` 預設 `30`
- `MAX_DROP_PARTITIONS_PER_RUN` 預設 `30`
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
- 單次最多建立 `MAX_CREATE_PARTITIONS_PER_RUN` 個 partition
- 單次最多刪除 `MAX_DROP_PARTITIONS_PER_RUN` 個 partition
- 若本次刪除會把所有非 `pmax` partition 全部清空，程式會拒絕執行
- DDL 完成後會重新查詢 `INFORMATION_SCHEMA.PARTITIONS`，確認建立或刪除結果符合預期

## 範例 SQL
```sql
CREATE TABLE user_events (
    id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    payload JSON NULL,
    created_at DATETIME NOT NULL,
    event_date DATE NOT NULL,
    PRIMARY KEY (id, event_date),
    KEY idx_user_id_event_date (user_id, event_date),
    KEY idx_event_date (event_date)
)
PARTITION BY RANGE (TO_DAYS(event_date)) (
    PARTITION p20260401 VALUES LESS THAN (TO_DAYS('2026-04-02')),
    PARTITION p20260402 VALUES LESS THAN (TO_DAYS('2026-04-03')),
    PARTITION pmax VALUES LESS THAN MAXVALUE
);
```
