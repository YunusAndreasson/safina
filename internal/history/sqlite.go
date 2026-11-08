//nolint:noctx // Local journal I/O must complete after API cancellation; SQLite lock waits have a busy timeout.
package history

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YunusAndreasson/safina/internal/localstate"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	databaseName = "history.sqlite"
	stampLayout  = "2006-01-02T15:04:05.000000000Z"
)

func openDatabase(dir string, readOnly bool) (*sql.DB, error) {
	path, err := filepath.Abs(filepath.Join(dir, databaseName))
	if err != nil {
		return nil, err
	}
	mode := "ro"
	if readOnly {
		info, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("unsafe history database file")
		}
	}
	if !readOnly {
		if err = localstate.Directory(dir); err != nil {
			return nil, err
		}
		f, e := localstate.Open(path, unix.O_CREAT|unix.O_RDWR)
		if e != nil {
			return nil, e
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
		mode = "rw"
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(FULL)")
	if !readOnly {
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	err = db.QueryRow("PRAGMA user_version").Scan(&version)
	if err == nil && version != 0 && version != 1 {
		err = errors.New("unsupported history database version")
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if readOnly {
		if version != 1 {
			err = errors.New("history migration incomplete; reopen Safina to retry")
		}
	} else {
		_, err = db.Exec(`PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS records (
 id TEXT PRIMARY KEY, time TEXT NOT NULL, session TEXT NOT NULL, sequence INTEGER NOT NULL,
 cluster TEXT NOT NULL, context TEXT NOT NULL, namespace TEXT NOT NULL,
 name TEXT NOT NULL, resource TEXT NOT NULL, workload TEXT NOT NULL, workload_resource TEXT NOT NULL,
 operation TEXT NOT NULL, gap INTEGER NOT NULL, data BLOB NOT NULL, bytes INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS records_time ON records(time, session, sequence, id);
 CREATE INDEX IF NOT EXISTS records_cluster_time ON records(cluster, time);
 CREATE INDEX IF NOT EXISTS records_context_time ON records(context, time);
 CREATE INDEX IF NOT EXISTS records_operation ON records(operation);
 CREATE INDEX IF NOT EXISTS records_gaps ON records(session) WHERE gap=1;
 CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
 INSERT OR IGNORE INTO metadata VALUES ('bytes','0');
 CREATE TRIGGER IF NOT EXISTS records_insert AFTER INSERT ON records BEGIN
 UPDATE metadata SET value=CAST(value AS INTEGER)+NEW.bytes WHERE key='bytes'; END;
 CREATE TRIGGER IF NOT EXISTS records_delete AFTER DELETE ON records BEGIN
 UPDATE metadata SET value=CAST(value AS INTEGER)-OLD.bytes WHERE key='bytes'; END;
 PRAGMA user_version=1;`)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !readOnly {
		if err = localstate.SyncDirectory(dir); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

func insertRecord(tx *sql.Tx, r Record, data []byte) error {
	var saved []byte
	err := tx.QueryRow("SELECT data FROM records WHERE id=?", r.ID).Scan(&saved)
	if err == nil {
		if !bytes.Equal(saved, data) {
			return errors.New("stable history event ID collision")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(`INSERT INTO records VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Time.UTC().Format(stampLayout), r.Session, r.Sequence, r.Target.Cluster, r.Target.Context, r.Target.Namespace, r.Target.Name, strings.ToLower(r.Target.Kind+"/"+r.Target.Name), r.Target.WorkloadName, strings.ToLower(r.Target.WorkloadKind+"/"+r.Target.WorkloadName), r.Operation, r.CoverageGap, data, len(data)+1)
	return err
}

func (s *Store) writeRecord(r Record, data []byte) error {
	if s.db == nil {
		return errors.New("history database unavailable")
	}
	if int64(len(data)+1) > s.opts.ActionBytes {
		return errors.New("actions capacity reached; recording incomplete")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertRecord(tx, r, data); err != nil {
		return err
	}
	if err = s.trimRecords(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) trimRecords(tx *sql.Tx) error {
	var total int64
	if err := tx.QueryRow("SELECT value FROM metadata WHERE key='bytes'").Scan(&total); err != nil {
		return err
	}
	if total <= s.opts.ActionBytes {
		return nil
	}
	// Reclaim only the oldest rows needed. A window over the whole journal
	// would turn every append at capacity into a full scan.
	for total > s.opts.ActionBytes {
		ids, reclaimed, err := oldestRecords(tx, total-s.opts.ActionBytes)
		if err != nil {
			return err
		}
		total -= reclaimed

		if len(ids) == 0 {
			return errors.New("history payload accounting inconsistent")
		}
		for _, id := range ids {
			if _, err = tx.Exec("DELETE FROM records WHERE id=?", id); err != nil {
				return err
			}
		}
	}

	return retentionMarker(tx)
}

func oldestRecords(tx *sql.Tx, needed int64) ([]string, int64, error) {
	rows, err := tx.Query("SELECT id, bytes FROM records ORDER BY time, session, sequence, id LIMIT 128")
	if err != nil {
		return nil, 0, err
	}
	defer func(r *sql.Rows) { _ = r.Close() }(rows)
	var ids []string
	var reclaimed int64
	for rows.Next() {
		var id string
		var size int64
		if err = rows.Scan(&id, &size); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
		reclaimed += size
		if reclaimed >= needed {
			break
		}
	}
	return ids, reclaimed, rows.Err()
}

func retentionMarker(tx *sql.Tx) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO metadata VALUES ('retention',?)`, time.Now().UTC().Format(time.RFC3339Nano)+": actions expired or capacity-pruned; earlier history may be unavailable")
	return err
}

func (s *Store) pruneRecords() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec("DELETE FROM records WHERE time<?", time.Now().AddDate(0, 0, -s.opts.ActionDays).UTC().Format(stampLayout))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		if err = retentionMarker(tx); err != nil {
			return err
		}
	}
	if err = s.trimRecords(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// The import marker and records commit together. Originals remain available
// for recovery, but are never read again after migration (including after prune).
func (s *Store) importLegacy() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var marker string
	err = tx.QueryRow("SELECT value FROM metadata WHERE key='legacy-import'").Scan(&marker)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	root, err := os.OpenRoot(s.opts.Directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = filepath.WalkDir(filepath.Join(s.opts.Directory, "actions"), func(path string, d fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		rel, e := filepath.Rel(s.opts.Directory, path)
		if e != nil {
			return e
		}
		f, e := root.Open(rel)
		if e != nil {
			return e
		}
		defer func() { _ = f.Close() }()
		// Refuse migration while a previous version still owns a segment.
		if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
			return fmt.Errorf("close older Safina sessions before migrating history: %w", e)
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 2<<20)
		warning := false
		for scan.Scan() {
			var r Record
			if json.Unmarshal(scan.Bytes(), &r) != nil || r.Version != 1 || r.ID == "" {
				warning = true
				continue
			}
			data, marshalErr := json.Marshal(r)
			if marshalErr != nil {
				return marshalErr
			}
			if e = insertRecord(tx, r, data); e != nil {
				return e
			}
		}
		if scan.Err() != nil {
			return scan.Err()
		}
		if warning {
			_, e = tx.Exec(`INSERT OR REPLACE INTO metadata VALUES ('import-warning',?)`, "Incomplete or unsupported legacy records; original JSONL retained in actions/")
		}
		return e
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO metadata VALUES ('legacy-import','1')`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func Query(dir string, f Filter) (Result, error) {
	if _, err := os.Stat(filepath.Join(dir, databaseName)); errors.Is(err, os.ErrNotExist) {
		return queryLegacy(dir, f)
	} else if err != nil {
		return Result{}, err
	}
	db, err := openDatabase(dir, true)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = db.Close() }()
	// A read transaction keeps count, rows and warnings on the same snapshot.
	tx, err := db.Begin()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var imported string
	if err = tx.QueryRow("SELECT value FROM metadata WHERE key='legacy-import'").Scan(&imported); err != nil {
		return Result{}, fmt.Errorf("history migration incomplete; reopen Safina to retry: %w", err)
	}
	where := "1=1"
	args := []any{}
	add := func(clause string, values ...any) { where += " AND (" + clause + ")"; args = append(args, values...) }
	if f.UID != "" {
		add("json_extract(data,'$.target.uid')=? OR json_extract(data,'$.target.workloadUID')=?", f.UID, f.UID)
	}
	if len(f.Actions) > 0 {
		values := make([]any, len(f.Actions))
		for i, v := range f.Actions {
			values[i] = v
		}
		add("json_extract(data,'$.action') IN ("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+")", values...)
	}
	if f.Cluster != "" {
		add("cluster=? OR context=?", f.Cluster, f.Cluster)
	}
	if f.Namespace != "" {
		add("namespace=?", f.Namespace)
	}
	if f.Resource != "" {
		add("name=? OR resource=? OR workload=? OR workload_resource=?", f.Resource, strings.ToLower(f.Resource), f.Resource, strings.ToLower(f.Resource))
	}
	if f.Session != "" {
		add("session=?", f.Session)
	}
	if f.ID != "" {
		add("id=? OR operation=?", f.ID, f.ID)
	}
	if !f.Since.IsZero() {
		add("time>=?", f.Since.UTC().Format(stampLayout))
	}
	if !f.Until.IsZero() {
		add("time<=?", f.Until.UTC().Format(stampLayout))
	}
	var result Result
	// SQL fragments are fixed above; all caller values are bound parameters.
	if err = tx.QueryRow("SELECT count(*) FROM records WHERE "+where, args...).Scan(&result.Omitted); err != nil {
		return result, err
	}
	if f.Limit <= 0 {
		f.Limit = 10000
	}
	result.Records, err = selectRecords(tx, where, args, f.Limit)
	if err != nil {
		return result, err
	}
	result.Omitted -= len(result.Records)
	sortRecords(result.Records)
	result.Warnings, err = selectWarnings(tx)
	if err != nil {
		return result, err
	}
	for _, pool := range []string{"actions", "evidence"} {
		if b, e := os.ReadFile(filepath.Join(dir, pool+"-retention.txt")); e == nil { // #nosec G304 -- Fixed retention filenames beneath the operator-selected history directory.
			result.Warnings = append(result.Warnings, string(b))
		}
	}
	return result, err
}

func selectRecords(tx *sql.Tx, where string, args []any, limit int) ([]Record, error) {
	rows, err := tx.Query("SELECT data FROM records WHERE "+where+" ORDER BY time DESC, session DESC, sequence DESC, id DESC LIMIT ?", append(args, limit)...) // #nosec G202 -- Query builds WHERE only from fixed clauses; all caller values are bound parameters.
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []Record
	for rows.Next() {
		var b []byte
		var r Record
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func selectWarnings(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT value FROM metadata WHERE key IN ('retention','import-warning') UNION ALL SELECT 'Recording gaps in session '||session FROM records WHERE gap=1 GROUP BY session`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var warnings []string
	for rows.Next() {
		var warning string
		if err = rows.Scan(&warning); err != nil {
			return nil, err
		}
		warnings = append(warnings, warning)
	}
	return warnings, rows.Err()
}
