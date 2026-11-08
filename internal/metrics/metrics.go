// Package metrics holds bounded, session-local resource observations.
package metrics

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
)

const (
	Available    = "available"
	Unavailable  = "unavailable"
	Partial      = "partial"
	Forbidden    = "forbidden"
	Unsupported  = "unsupported"
	Invalid      = "invalid"
	MaxPoints    = 262144
	MaxResources = 2048
	PerResource  = 4320
	Retention    = 6 * time.Hour
)

type Reading struct {
	CPU, Memory                                int64
	SourceAt                                   time.Time
	Window                                     time.Duration
	State                                      string
	Covered, Expected                          int
	Fingerprint                                uint64
	ContainerNames                             string
	CPURequest, CPULimit, MemRequest, MemLimit int64
}

func (r Reading) Valid() bool             { return r.State == Available }
func (r Reading) GapAfter() time.Duration { return max(30*time.Second, 2*r.Window) }
func (r Reading) Stale(now time.Time) bool {
	return !r.SourceAt.IsZero() && now.Sub(r.SourceAt) > max(time.Minute, 2*r.Window)
}

func (r Reading) Status(now time.Time) string {
	if r.State == "" {
		return Unavailable
	}
	if r.Valid() && r.Stale(now) {
		return "stale"
	}
	return r.State
}

type Point struct {
	At time.Time
	Reading
}
type (
	Key struct {
		Cluster, Kind, Namespace, Name string
		UID                            types.UID
	}
	Series struct {
		Key             Key
		Points          []Point
		Latest          Point
		Revision        uint64
		LastSeen        time.Time
		lastSource      time.Time
		lastFingerprint uint64
	}
)

func (s *Series) Window(start, end time.Time) []Point {
	if s == nil {
		return nil
	}
	lo, hi := 0, len(s.Points)
	for lo < hi && s.Points[lo].At.Before(start) {
		lo++
	}
	for hi > lo && s.Points[hi-1].At.After(end) {
		hi--
	}
	return s.Points[lo:hi]
}

type Store struct {
	Series    map[Key]*Series
	Protected Key
	count     int
}

func (s *Store) Reset() { *s = Store{} }
func (s *Store) Add(key Key, r Reading, now time.Time) *Series {
	if s.Series == nil {
		s.Series = make(map[Key]*Series)
	}
	series := s.Series[key]
	if series == nil {
		series = &Series{Key: key}
		s.Series[key] = series
	}
	series.LastSeen = now
	// Repeated source samples update freshness/coverage, but not the time axis.
	duplicate := len(series.Points) > 0 && series.Latest.Reading == r
	if r.Valid() && !r.SourceAt.IsZero() && r.SourceAt.Equal(series.lastSource) && r.Fingerprint == series.lastFingerprint {
		duplicate = true
	}
	if series.Latest.Reading != r {
		series.Revision++
	}
	if r.Valid() {
		series.lastSource, series.lastFingerprint = r.SourceAt, r.Fingerprint
	}
	series.Latest = Point{At: now, Reading: r}
	if !duplicate {
		series.Points = append(series.Points, series.Latest)
		s.count++
		series.Revision++
	}
	n := 0
	for n < len(series.Points) && (series.Points[n].At.Before(now.Add(-Retention)) || len(series.Points)-n > PerResource) {
		n++
	}
	if n > 0 {
		s.count -= n
		copy(series.Points, series.Points[n:])
		clear(series.Points[len(series.Points)-n:])
		series.Points = series.Points[:len(series.Points)-n]
		series.Revision++
	}
	for s.count > MaxPoints || len(s.Series) > MaxResources {
		var oldest *Series
		for k, v := range s.Series {
			if k == s.Protected || k == key {
				continue
			}
			if oldest == nil || v.LastSeen.Before(oldest.LastSeen) {
				oldest = v
			}
		}
		if oldest == nil {
			break
		}
		s.count -= len(oldest.Points)
		delete(s.Series, oldest.Key)
	}
	return series
}

func (s *Store) Expire(now time.Time) {
	for k, v := range s.Series {
		n := 0
		for n < len(v.Points) && (v.Points[n].At.Before(now.Add(-Retention)) || len(v.Points)-n > PerResource) {
			n++
		}
		if n > 0 {
			s.count -= n
			copy(v.Points, v.Points[n:])
			clear(v.Points[len(v.Points)-n:])
			v.Points = v.Points[:len(v.Points)-n]
			v.Revision++
		}
		if k != s.Protected && now.Sub(v.LastSeen) > Retention {
			s.count -= len(v.Points)
			delete(s.Series, k)
		}
	}
}
