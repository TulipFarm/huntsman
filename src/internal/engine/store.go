package engine

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db       *sql.DB
	lock     *os.File
	mu       sync.Mutex
	capacity int
}

func OpenStore(path string, capacity int) (*Store, error) {
	if capacity < 1 {
		return nil, errors.New("queue capacity must be positive")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("database is already in use: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: lock, capacity: capacity}
	_, err = db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS jobs (
 id TEXT PRIMARY KEY, key TEXT NOT NULL, url TEXT NOT NULL, host TEXT NOT NULL,
 request BLOB NOT NULL, status TEXT NOT NULL, result BLOB, error TEXT NOT NULL DEFAULT '',
 cached INTEGER NOT NULL DEFAULT 0, crawl_id TEXT NOT NULL DEFAULT '', depth INTEGER NOT NULL DEFAULT 0,
 created INTEGER NOT NULL, updated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS job_queue ON jobs(status,created);
CREATE INDEX IF NOT EXISTS job_host ON jobs(host,status);
CREATE INDEX IF NOT EXISTS job_key ON jobs(key,status);
CREATE UNIQUE INDEX IF NOT EXISTS crawl_url ON jobs(crawl_id,url) WHERE crawl_id!='';
CREATE TABLE IF NOT EXISTS cache (key TEXT PRIMARY KEY, result BLOB NOT NULL, fetched INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS crawls (id TEXT PRIMARY KEY, request BLOB NOT NULL, created INTEGER NOT NULL);
UPDATE jobs SET status='queued' WHERE status='running';`)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	err := s.db.Close()
	lockErr := s.lock.Close()
	return errors.Join(err, lockErr)
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

const jobColumns = "id,status,request,result,error,cached,crawl_id,depth"

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (Job, error) {
	var j Job
	var req, result []byte
	err := row.Scan(&j.ID, &j.Status, &req, &result, &j.Error, &j.Cached, &j.CrawlID, &j.Depth)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(req, &j.Request); err != nil {
		return j, err
	}
	if len(result) > 0 {
		if err = json.Unmarshal(result, &j.Data); err != nil {
			return j, err
		}
	}
	return j, nil
}

func (s *Store) Get(id string) (Job, error) {
	return scanJob(s.db.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
}

func (s *Store) Submit(requests []Request) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	jobs := make([]Job, 0, len(requests))
	for _, r := range requests {
		j, err := s.insert(tx, r, "", 0)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, tx.Commit()
}

func (s *Store) insert(tx *sql.Tx, r Request, crawlID string, depth int) (Job, error) {
	key := r.key()
	if crawlID == "" {
		j, err := scanJob(tx.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE key=? AND crawl_id='' AND status IN ('queued','running') LIMIT 1", key))
		if err == nil {
			return j, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return j, err
		}
	}
	j := Job{ID: newID(), Status: "queued", Request: r, CrawlID: crawlID, Depth: depth}
	var cached []byte
	if *r.MaxAge > 0 {
		err := tx.QueryRow("SELECT result FROM cache WHERE key=? AND fetched>=?", key, time.Now().UnixMilli()-*r.MaxAge).Scan(&cached)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return j, err
		}
		if len(cached) > 0 && crawlID == "" {
			if err := json.Unmarshal(cached, &j.Data); err != nil {
				return j, err
			}
			j.Status, j.Cached = "completed", true
		}
	}
	if j.Status == "queued" {
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('queued','running')").Scan(&n); err != nil {
			return j, err
		}
		if n >= s.capacity {
			return j, ErrFull
		}
	}
	req, err := json.Marshal(r)
	if err != nil {
		return j, err
	}
	now := time.Now().UnixMilli()
	_, err = tx.Exec("INSERT INTO jobs(id,key,url,host,request,status,result,cached,crawl_id,depth,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
		j.ID, key, r.URL, r.host(), req, j.Status, nullable(cached, j.Cached), j.Cached, crawlID, depth, now, now)
	return j, err
}

func nullable(b []byte, use bool) any {
	if use {
		return b
	}
	return nil
}

func (s *Store) Claim(perHost int) (Job, error) {
	return scanJob(s.db.QueryRow(`UPDATE jobs SET status='running',updated=? WHERE id=(
SELECT q.id FROM jobs q WHERE q.status='queued'
AND (SELECT count(*) FROM jobs r WHERE r.status='running' AND r.host=q.host) < ?
ORDER BY q.created,q.id LIMIT 1) RETURNING `+jobColumns, time.Now().UnixMilli(), perHost))
}

func (s *Store) Cached(r Request) (*Document, error) {
	if *r.MaxAge == 0 {
		return nil, nil
	}
	var raw []byte
	err := s.db.QueryRow("SELECT result FROM cache WHERE key=? AND fetched>=?", r.key(), time.Now().UnixMilli()-*r.MaxAge).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc Document
	err = json.Unmarshal(raw, &doc)
	return &doc, err
}

func (s *Store) Finish(j Job, doc *Document, renderErr error, cached bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status, message := "completed", ""
	var result []byte
	if renderErr != nil {
		status, message = "failed", renderErr.Error()
	} else {
		result, err = json.Marshal(doc)
		if err != nil {
			return err
		}
	}
	now := time.Now().UnixMilli()
	if _, err = tx.Exec("UPDATE jobs SET status=?,result=?,error=?,cached=?,updated=? WHERE id=? AND status='running'",
		status, result, message, cached, now, j.ID); err != nil {
		return err
	}
	if renderErr == nil && doc.Metadata.StatusCode < 400 {
		if _, err = tx.Exec("INSERT INTO cache(key,result,fetched) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET result=excluded.result,fetched=excluded.fetched",
			j.Request.key(), result, doc.Metadata.FetchedAt.UnixMilli()); err != nil {
			return err
		}
		if j.CrawlID != "" {
			if err = s.expand(tx, j, doc.Links); err != nil {
				if !errors.Is(err, ErrFull) {
					return err
				}
				if _, err = tx.Exec("UPDATE jobs SET status='failed',error=? WHERE id=?",
					"crawl expansion stopped because the queue is full", j.ID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

func (s *Store) Prune() error {
	_, err := s.db.Exec(`
DELETE FROM cache WHERE fetched < ?;
DELETE FROM cache WHERE key IN (SELECT key FROM cache ORDER BY fetched DESC LIMIT -1 OFFSET 10000);
DELETE FROM jobs WHERE status IN ('completed','failed') AND updated < ?;
DELETE FROM jobs WHERE id IN (SELECT id FROM jobs WHERE crawl_id='' AND status IN ('completed','failed') ORDER BY updated DESC LIMIT -1 OFFSET 10000);
DELETE FROM crawls WHERE created < ? AND NOT EXISTS (SELECT 1 FROM jobs WHERE crawl_id=crawls.id);`,
		time.Now().Add(-7*24*time.Hour).UnixMilli(), time.Now().Add(-7*24*time.Hour).UnixMilli(), time.Now().Add(-7*24*time.Hour).UnixMilli())
	return err
}
