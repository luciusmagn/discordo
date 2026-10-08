package history

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"go.etcd.io/bbolt"
)

var (
	messagesBucket = []byte("messages")
	pagesBucket    = []byte("pages")
)

type Store struct {
	db *bbolt.DB
	mu sync.RWMutex
}

type page struct {
	Before    uint64              `json:"before"`
	Limit     uint                `json:"limit"`
	IDs       []discord.MessageID `json:"ids"`
	Low       uint64              `json:"low"`
	High      uint64              `json:"high"`
	Exhausted bool                `json:"exhausted"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	if err = db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(messagesBucket)
		if e != nil {
			return e
		}
		_, e = tx.CreateBucketIfNotExists(pagesBucket)
		return e
	}); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	// Sync directories too, including any ancestors just created by MkdirAll.
	if runtime.GOOS != "windows" {
		for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
			dir, err := os.Open(directory)
			if err != nil {
				db.Close()
				return nil, err
			}
			err = dir.Sync()
			dir.Close()
			if err != nil {
				db.Close()
				return nil, err
			}
			if directory == filepath.Dir(directory) {
				break
			}
		}
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
func channelKey(id discord.ChannelID) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(id))
	return b
}
func messageKey(id discord.MessageID) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(id))
	return b
}
func clean(m discord.Message) discord.Message { m.Reactions = nil; return m }
func (s *Store) Put(ms []discord.Message) error {
	vals := make([][]byte, len(ms))
	for i, m := range ms {
		var e error
		vals[i], e = json.Marshal(clean(m))
		if e != nil {
			return e
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		root := tx.Bucket(messagesBucket)
		for i, m := range ms {
			cb, e := root.CreateBucketIfNotExists(channelKey(m.ChannelID))
			if e != nil {
				return e
			}
			if e = cb.Put(messageKey(m.ID), vals[i]); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Message(ch discord.ChannelID, id discord.MessageID) (*discord.Message, error) {
	var out *discord.Message
	s.mu.RLock()
	defer s.mu.RUnlock()
	err := s.db.View(func(tx *bbolt.Tx) error {
		root := tx.Bucket(messagesBucket)
		cb := root.Bucket(channelKey(ch))
		if cb == nil {
			return nil
		}
		v := cb.Get(messageKey(id))
		if v == nil {
			return nil
		}
		var m discord.Message
		if e := json.Unmarshal(v, &m); e != nil {
			return e
		}
		out = &m
		return nil
	})
	return out, err
}
func (s *Store) MessagesBefore(ch discord.ChannelID, before discord.MessageID, limit uint) ([]discord.Message, error) {
	if limit == 0 {
		return []discord.Message{}, nil
	}
	out := []discord.Message{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	err := s.db.View(func(tx *bbolt.Tx) error {
		root := tx.Bucket(messagesBucket)
		cb := root.Bucket(channelKey(ch))
		if cb == nil {
			return nil
		}
		cursor := cb.Cursor()
		key, value := cursor.Last()
		if before != 0 {
			key, value = cursor.Seek(messageKey(before))
			if key == nil {
				key, value = cursor.Last()
			} else {
				key, value = cursor.Prev()
			}
		}
		for key != nil && len(out) < int(limit) {
			var message discord.Message
			if err := json.Unmarshal(value, &message); err != nil {
				return err
			}
			out = append(out, message)
			key, value = cursor.Prev()
		}
		return nil
	})
	return out, err
}
func pageKey(p page) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b, p.Before)
	binary.BigEndian.PutUint64(b[8:], uint64(p.Limit))
	return b
}
func (s *Store) SavePage(ch discord.ChannelID, before discord.MessageID, limit uint, ms []discord.Message) error {
	cp := make([]discord.Message, len(ms))
	ids := make([]discord.MessageID, len(ms))
	for i, m := range ms {
		cp[i] = clean(m)
		ids[i] = m.ID
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID > cp[j].ID })
	raw := make([][]byte, len(cp))
	for i, m := range cp {
		var e error
		raw[i], e = json.Marshal(m)
		if e != nil {
			return e
		}
	}
	p := page{Before: uint64(before), High: uint64(before), Limit: limit, IDs: ids, Exhausted: limit > 0 && uint(len(ms)) < limit}
	if len(ids) > 0 {
		p.Low = uint64(ids[len(ids)-1])
		if before != 0 {
			p.High = uint64(before)
		} else {
			p.High = uint64(ids[0]) + 1
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		root := tx.Bucket(messagesBucket)
		cb, e := root.CreateBucketIfNotExists(channelKey(ch))
		if e != nil {
			return e
		}
		for i, m := range cp {
			if e = cb.Put(messageKey(m.ID), raw[i]); e != nil {
				return e
			}
		}
		pb := tx.Bucket(pagesBucket)
		c, e := pb.CreateBucketIfNotExists(channelKey(ch))
		if e != nil {
			return e
		}
		if before == 0 {
			var latestKeys [][]byte
			var archived []page
			if err := c.ForEach(func(key, value []byte) error {
				var old page
				if err := json.Unmarshal(value, &old); err != nil {
					return err
				}
				if old.Before != 0 {
					return nil
				}
				latestKeys = append(latestKeys, append([]byte(nil), key...))
				if old.High >= p.Low && old.Low <= p.High && len(p.IDs) > 0 {
					// Keep the older coverage when the in-memory tail is truncated.
					p.Low = min(p.Low, old.Low)
					p.Exhausted = p.Exhausted || old.Exhausted
				} else if old.High > 0 {
					// A disconnected new head must not erase a previously fetched range.
					old.Before = old.High
					archived = append(archived, old)
				}
				return nil
			}); err != nil {
				return err
			}
			for _, key := range latestKeys {
				if err := c.Delete(key); err != nil {
					return err
				}
			}
			for _, old := range archived {
				data, err := json.Marshal(old)
				if err != nil {
					return err
				}
				if err := c.Put(pageKey(old), data); err != nil {
					return err
				}
			}
		}
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return c.Put(pageKey(p), data)
	})
}

// CachedPage returns the contiguous saved prefix below before. A complete page
// contains the requested count, or reaches the known beginning of the channel.
func (s *Store) CachedPage(ch discord.ChannelID, before discord.MessageID, limit uint) ([]discord.Message, bool, error) {
	if limit == 0 {
		return nil, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var rows []discord.Message
	var complete bool
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(pagesBucket).Bucket(channelKey(ch))
		if bucket == nil {
			return nil
		}
		var pages []page
		upper := uint64(before)
		latestFound := before != 0
		if err := bucket.ForEach(func(_, value []byte) error {
			var saved page
			if err := json.Unmarshal(value, &saved); err != nil {
				return err
			}
			pages = append(pages, saved)
			if before == 0 && saved.Before == 0 {
				upper = saved.High
				latestFound = true
				if len(saved.IDs) == 0 && saved.Exhausted {
					complete = true
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if !latestFound || complete {
			return nil
		}
		// Extend coverage through overlapping pages, but never through an unknown gap.
		lower := upper
		for {
			previous := lower
			for _, saved := range pages {
				low := saved.Low
				if saved.Exhausted {
					low = 0
				}
				if saved.High >= lower && low < lower {
					lower = low
				}
			}
			if lower == previous {
				break
			}
		}
		if lower == upper {
			return nil
		}
		messages := tx.Bucket(messagesBucket).Bucket(channelKey(ch))
		if messages != nil {
			cursor := messages.Cursor()
			key, value := cursor.Seek(messageKey(discord.MessageID(upper)))
			if key == nil {
				key, value = cursor.Last()
			} else {
				key, value = cursor.Prev()
			}
			for key != nil && binary.BigEndian.Uint64(key) >= lower && uint(len(rows)) < limit {
				var message discord.Message
				if err := json.Unmarshal(value, &message); err != nil {
					return err
				}
				rows = append(rows, message)
				key, value = cursor.Prev()
			}
		}
		complete = uint(len(rows)) >= limit || lower == 0
		return nil
	})
	return rows, complete, err
}
func (s *Store) Delete(ch discord.ChannelID, ids []discord.MessageID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		root := tx.Bucket(messagesBucket)
		if cb := root.Bucket(channelKey(ch)); cb != nil {
			for _, id := range ids {
				if e := cb.Delete(messageKey(id)); e != nil {
					return e
				}
			}
		}
		pb := tx.Bucket(pagesBucket).Bucket(channelKey(ch))
		if pb == nil {
			return nil
		}
		var del [][]byte
		e := pb.ForEach(func(k, v []byte) error {
			var p page
			if e := json.Unmarshal(v, &p); e != nil {
				return e
			}
			for _, x := range p.IDs {
				for _, id := range ids {
					if x == id {
						del = append(del, append([]byte(nil), k...))
						return nil
					}
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
		for _, k := range del {
			if e = pb.Delete(k); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) InvalidateLatest(ch discord.ChannelID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		pb := tx.Bucket(pagesBucket).Bucket(channelKey(ch))
		if pb == nil {
			return nil
		}
		var del [][]byte
		e := pb.ForEach(func(k, v []byte) error {
			var p page
			if e := json.Unmarshal(v, &p); e != nil {
				return e
			}
			if p.Before == 0 {
				del = append(del, append([]byte(nil), k...))
			}
			return nil
		})
		if e != nil {
			return e
		}
		for _, k := range del {
			if e = pb.Delete(k); e != nil {
				return e
			}
		}
		return nil
	})
}
