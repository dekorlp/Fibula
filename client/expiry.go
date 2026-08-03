package client

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// The thinning schedule of E14: fine granularity for yesterday, a weekly state
// three months ago. It fits the purpose "safety net" better than a hard
// cutoff, which would either keep far too much or lose the recent detail that
// is the whole point.
const (
	RetainHourlyFor = 24 * time.Hour
	RetainDailyFor  = 30 * 24 * time.Hour
	RetainWeeklyFor = 182 * 24 * time.Hour // roughly six months
)

// Retention decides which snapshots the schedule keeps.
type Retention struct {
	Hourly time.Duration
	Daily  time.Duration
	Weekly time.Duration
}

// DefaultRetention is the schedule of E14.
func DefaultRetention() Retention {
	return Retention{Hourly: RetainHourlyFor, Daily: RetainDailyFor, Weekly: RetainWeeklyFor}
}

// Retain partitions a timeline into the snapshots to keep and the ones the
// schedule drops.
//
// One snapshot survives per bucket, and it is the newest in that bucket: when
// only one state per day can be kept, the most recent one is the most useful.
// Anything older than the weekly window is dropped entirely.
//
// Deliberate versions never appear here. They are not on the timeline at all,
// which is what makes E14's hard coupling — expiring a snapshot never deletes
// a chunk a reachable deliberate version references — a property of the data
// model rather than a rule the pruner has to remember.
func (r Retention) Retain(timeline []Snapshot, now time.Time) (keep, drop []Snapshot) {
	// Sorted here rather than assumed. Timeline returns chronological order,
	// but a function whose result silently depends on the caller having sorted
	// its input is a function that will one day be called wrongly - and this
	// one decides what gets thrown away.
	ordered := append([]Snapshot(nil), timeline...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time.Before(ordered[j].Time) })

	seen := make(map[string]struct{}, len(ordered))

	// Newest first, so the first snapshot met in a bucket is the one kept.
	for i := len(ordered) - 1; i >= 0; i-- {
		snapshot := ordered[i]

		bucket, within := r.bucketOf(snapshot.Time, now)
		if !within {
			drop = append(drop, snapshot)
			continue
		}
		if _, taken := seen[bucket]; taken {
			drop = append(drop, snapshot)
			continue
		}
		seen[bucket] = struct{}{}
		keep = append(keep, snapshot)
	}

	reverse(keep)
	reverse(drop)
	return keep, drop
}

// bucketOf names the slot a snapshot competes in, and reports whether it is
// inside the retention window at all.
func (r Retention) bucketOf(at, now time.Time) (string, bool) {
	age := now.Sub(at)

	switch {
	case age < 0:
		// A snapshot from the future is a clock jump, not a reason to delete
		// anything. It keeps its own bucket.
		return "future/" + at.UTC().Format(time.RFC3339), true
	case age <= r.Hourly:
		return "hour/" + at.UTC().Format("2006-01-02T15"), true
	case age <= r.Daily:
		return "day/" + at.UTC().Format("2006-01-02"), true
	case age <= r.Weekly:
		year, week := at.UTC().ISOWeek()
		return fmt.Sprintf("week/%d-%02d", year, week), true
	default:
		return "", false
	}
}

func reverse(s []Snapshot) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// ExpireResult reports what the schedule dropped.
type ExpireResult struct {
	Kept    int
	Expired []Snapshot
}

// Expire applies the thinning schedule to the snapshot timeline (F-S4-05).
//
// It deletes refs and nothing else. The version objects and the chunks they
// reference stay in the store until garbage collection runs and finds them
// unreachable — expiry decides what is still wanted, garbage collection
// decides what may be removed, and keeping those two apart is what makes
// either of them reviewable.
func (s *Space) Expire(ctx context.Context, r Retention, now time.Time) (ExpireResult, error) {
	timeline, err := s.Timeline(ctx)
	if err != nil {
		return ExpireResult{}, err
	}

	keep, drop := r.Retain(timeline, now)
	for _, snapshot := range drop {
		if err := s.refs.Delete(ctx, snapshot.Ref); err != nil {
			return ExpireResult{}, fmt.Errorf("expire %s: %w", snapshot.Ref, err)
		}
	}
	return ExpireResult{Kept: len(keep), Expired: drop}, nil
}
