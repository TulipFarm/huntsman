package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

func (s *Store) StartCrawl(r CrawlRequest) (string, error) {
	if r.Limit < 1 || r.Limit > 1000 || r.MaxDepth < 0 || r.MaxDepth > 10 {
		return "", errors.New("crawl limit must be 1..1000 and maxDepth 0..10")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id := newID()
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec("INSERT INTO crawls(id,request,created) VALUES(?,?,?)", id, data, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	if _, err = s.insert(tx, r.ScrapeOptions, id, 0); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (s *Store) expand(tx *sql.Tx, j Job, links []string) error {
	var raw []byte
	if err := tx.QueryRow("SELECT request FROM crawls WHERE id=?", j.CrawlID).Scan(&raw); err != nil {
		return err
	}
	var r CrawlRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if j.Depth >= r.MaxDepth {
		return nil
	}
	root, _ := url.Parse(r.URL)
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM jobs WHERE crawl_id=?", j.CrawlID).Scan(&count); err != nil {
		return err
	}
	for _, link := range links {
		if count >= r.Limit {
			break
		}
		u, err := url.Parse(link)
		if err != nil || u.Scheme != root.Scheme || u.Host != root.Host || u.User != nil {
			continue
		}
		req := r.ScrapeOptions
		req.URL = link
		if err := req.Normalize(time.Hour); err != nil {
			continue
		}
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM jobs WHERE crawl_id=? AND url=?)", j.CrawlID, req.URL).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.insert(tx, req, j.CrawlID, j.Depth+1); err != nil {
			return err
		}
		count++
	}
	return nil
}

func (s *Store) Crawl(id string, offset int) (CrawlResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := CrawlResult{ID: id, Status: "running", Jobs: []Job{}}
	var exists bool
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM crawls WHERE id=?)", id).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, ErrNotFound
	}
	if err := s.db.QueryRow(`SELECT count(*),coalesce(sum(status='completed'),0),coalesce(sum(status='failed'),0)
FROM jobs WHERE crawl_id=?`, id).Scan(&out.Total, &out.Completed, &out.Failed); err != nil {
		return out, err
	}
	if out.Completed+out.Failed == out.Total {
		out.Status = "completed"
		if out.Failed > 0 {
			out.Status = "completed_with_errors"
		}
	}
	rows, err := s.db.Query("SELECT "+jobColumns+" FROM jobs WHERE crawl_id=? ORDER BY created,id LIMIT 100 OFFSET ?", id, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return out, err
		}
		out.Jobs = append(out.Jobs, j)
	}
	if offset+len(out.Jobs) < out.Total {
		next := offset + len(out.Jobs)
		out.Next = &next
	}
	return out, rows.Err()
}
