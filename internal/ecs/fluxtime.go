package ecs

import "time"

// fluxMaxAge is how old a point may be and still be published.
//
// Every measurement this collector queries writes points five minutes apart on
// a live 4.3, so twice that leaves one missed write of slack before a series
// goes absent. It must stay well under fluxRange: last() returns the newest
// point in the window whatever its age, and these samples carry no timestamp of
// their own, so Prometheus stamps them at scrape time. Without this guard a
// node that stopped emitting keeps a value that looks current for the full
// width of the window.
//
// A measurement in one of the store's slower cadence classes (10-25 minutes, or
// sparse) would need its own value — hence fluxQuery.maxAge.
const fluxMaxAge = 10 * time.Minute

// age returns how far in the past this row's point was written, and whether the
// row could be dated at all. A row with no _time, or one we cannot parse, is
// undatable: it cannot be shown to be current, so it is not published.
func (r fluxRow) age(now time.Time) (time.Duration, bool) {
	ts, ok := r.value("_time")
	if !ok {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0, false
	}
	return now.Sub(t), true
}

// maxAgeOrDefault is this query's staleness threshold.
func (q fluxQuery) maxAgeOrDefault() time.Duration {
	if q.maxAge > 0 {
		return q.maxAge
	}
	return fluxMaxAge
}
