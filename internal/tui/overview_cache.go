package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/localstate"
	"github.com/yunus/safina/internal/tui/sections"
)

const (
	overviewVersion       = 1
	overviewMaxEntries    = 4
	overviewMaxBytes      = 8 << 20
	overviewTTL           = 24 * time.Hour
	overviewWriteInterval = time.Minute
)

// Explicit projection: no manifests, annotations, credentials, secrets or
// metric samples belong in the persistent first-paint cache.
type overviewApp struct {
	Kind      string     `json:"kind"`
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	Health    k8s.Health `json:"health"`
}

type overviewNamespace struct {
	Name   string `json:"name"`
	Age    string `json:"age"`
	System bool   `json:"system"`
}

type overviewSnapshot struct {
	Key              string              `json:"key"`
	At               time.Time           `json:"at"`
	Namespaces       []overviewNamespace `json:"namespaces"`
	SystemNamespaces []string            `json:"systemNamespaces"`
	Apps             []overviewApp       `json:"apps"`
}

type overviewFile struct {
	Version int                `json:"version"`
	Entries []overviewSnapshot `json:"entries"`
}

type overviewStore struct {
	dir         string
	mu          sync.Mutex
	memory      []overviewSnapshot
	writeMu     sync.Mutex
	diskWritten map[string]time.Time
}

func newOverviewStore() *overviewStore {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	return &overviewStore{dir: filepath.Join(dir, "safina", "overviews")}
}

func (s *overviewStore) readFile() []overviewSnapshot {
	f, err := localstate.Open(filepath.Join(s.dir, "v1.json"), os.O_RDONLY)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, overviewMaxBytes+1))
	if err != nil || len(data) > overviewMaxBytes {
		return nil
	}
	var file overviewFile
	if json.Unmarshal(data, &file) != nil || file.Version != overviewVersion || len(file.Entries) > overviewMaxEntries {
		return nil
	}
	return file.Entries
}

func validOverview(v overviewSnapshot, now time.Time) bool {
	return v.Key != "" && !v.At.IsZero() && !v.At.After(now) && now.Sub(v.At) <= overviewTTL
}

func (s *overviewStore) load(key string, now time.Time) *overviewSnapshot {
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	for _, v := range s.memory {
		if v.Key == key && validOverview(v, now) {
			s.mu.Unlock()
			return &v
		}
	}
	s.mu.Unlock()
	for _, v := range s.readFile() {
		if v.Key == key && validOverview(v, now) {
			s.remember(v, now)
			return &v
		}
	}
	return nil
}

func mergeOverview(entries []overviewSnapshot, incoming overviewSnapshot, now time.Time) []overviewSnapshot {
	out := make([]overviewSnapshot, 0, overviewMaxEntries+1)
	for _, v := range entries {
		if !validOverview(v, now) {
			continue
		}
		if v.Key == incoming.Key {
			if v.At.After(incoming.At) {
				incoming = v
			}
			continue
		}
		out = append(out, v)
	}
	if validOverview(incoming, now) {
		out = append(out, incoming)
	}
	slices.SortFunc(out, func(a, b overviewSnapshot) int { return b.At.Compare(a.At) })
	if len(out) > overviewMaxEntries {
		clear(out[overviewMaxEntries:])
		out = out[:overviewMaxEntries]
	}
	return out
}

func (s *overviewStore) remember(v overviewSnapshot, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memory = mergeOverview(s.memory, v, now)
}

// save is best effort. The cache is disposable; failures must never delay a
// fresh UI result or interfere with application state/history persistence.
func (s *overviewStore) save(v overviewSnapshot) {
	s.saveAt(v, time.Now())
}

func (s *overviewStore) saveAt(v overviewSnapshot, now time.Time) {
	if s == nil || !validOverview(v, now) {
		return
	}
	// Bound individual entries before retaining their backing slices in memory.
	data, err := json.Marshal(v)
	if err != nil || len(data) > overviewMaxBytes/overviewMaxEntries {
		return
	}
	s.remember(v, now)
	// Memory follows every successful refresh. Persist at most once a minute
	// per cluster; a first visit still writes immediately. Never queue writers.
	if !s.writeMu.TryLock() {
		return
	}
	defer s.writeMu.Unlock()
	if !s.diskWriteDue(v.Key, now) {
		return
	}
	if localstate.Directory(s.dir) != nil {
		return
	}
	lock, err := localstate.Lock(filepath.Join(s.dir, "write.lock"))
	if err != nil {
		return
	}
	defer func() { _ = lock.Close() }()
	entries := mergeOverview(s.readFile(), v, now)
	data, err = json.Marshal(overviewFile{Version: overviewVersion, Entries: entries})
	for err == nil && len(data) > overviewMaxBytes && len(entries) > 0 {
		entries = entries[:len(entries)-1]
		data, err = json.Marshal(overviewFile{Version: overviewVersion, Entries: entries})
	}
	if err != nil {
		return
	}
	f, err := os.CreateTemp(s.dir, ".overview-*")
	if err != nil {
		return
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return
	}
	if os.Rename(path, filepath.Join(s.dir, "v1.json")) == nil {
		s.recordDiskWrite(v.Key, now)
	}
}

func (s *overviewStore) diskWriteDue(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.diskWritten[key]
	return !ok || now.Before(at) || now.Sub(at) >= overviewWriteInterval
}

func (s *overviewStore) recordDiskWrite(key string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.diskWritten == nil {
		s.diskWritten = make(map[string]time.Time)
	}
	// Keep write bookkeeping bounded along with the retained snapshots.
	for existing := range s.diskWritten {
		if !slices.ContainsFunc(s.memory, func(v overviewSnapshot) bool { return v.Key == existing }) {
			delete(s.diskWritten, existing)
		}
	}
	if slices.ContainsFunc(s.memory, func(v overviewSnapshot) bool { return v.Key == key }) {
		s.diskWritten[key] = now
	}
}

func (m *Model) overviewKey() string {
	if m.ctx.Manager == nil {
		return ""
	}
	client, ok := m.ctx.Manager.GetClient(m.ctx.ActiveContext)
	if !ok || client.RESTConfig == nil {
		return ""
	}
	cfg := client.RESTConfig
	// Hash identifiers only; the file never contains server URLs or auth data.
	// Namespace classification is part of the key so changed preferences cannot
	// restore the wrong division between user and system namespaces.
	var user, system []string
	if m.ctx.AppCfg != nil {
		user, system = m.ctx.AppCfg.UserNamespaces, m.ctx.AppCfg.SystemNamespaces
	}
	identity := []any{
		m.ctx.ActiveContext, cfg.Host, cfg.CAFile, cfg.Username, cfg.CAData, user, system,
		cfg.Impersonate.UserName, cfg.Impersonate.UID, cfg.Impersonate.Groups, cfg.Impersonate.Extra,
	}

	data, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func snapshotOverview(key string, at time.Time, ch *sections.Children) overviewSnapshot {
	// Very large overviews keep the normal load path instead of making an
	// unbounded secondary projection solely for a best-effort preview.
	if len(ch.Apps) > 20000 || len(ch.UserNS)+len(ch.SystemNS) > 20000 {
		return overviewSnapshot{}
	}
	v := overviewSnapshot{Key: key, At: at, Namespaces: make([]overviewNamespace, len(ch.UserNS)), SystemNamespaces: slices.Clone(ch.SystemNS), Apps: make([]overviewApp, len(ch.Apps))}
	for i, ns := range ch.UserNS {
		v.Namespaces[i] = overviewNamespace{Name: ns.Name, Age: ns.Age, System: ns.System}
	}
	for i := range ch.Apps {
		a := &ch.Apps[i]
		v.Apps[i] = overviewApp{Kind: a.Kind, Namespace: a.Namespace, Name: a.Name, Health: a.Health}
	}
	return v
}

func (v overviewSnapshot) children() *sections.Children {
	ch := &sections.Children{Kind: k8s.KindNamespaceFolder, UserNS: make([]k8s.NamespaceInfo, len(v.Namespaces)), SystemNS: slices.Clone(v.SystemNamespaces), SystemCount: len(v.SystemNamespaces), Apps: make([]k8s.AppInfo, len(v.Apps))}
	for i, ns := range v.Namespaces {
		ch.UserNS[i] = k8s.NamespaceInfo{Name: ns.Name, Age: ns.Age, System: ns.System}
	}
	for i, a := range v.Apps {
		ch.Apps[i] = k8s.AppInfo{Kind: a.Kind, Namespace: a.Namespace, Name: a.Name, Health: a.Health}
	}
	return ch
}
