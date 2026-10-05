package telegram

import (
	"sort"
	"time"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/schedule"
)

// nextFire returns the next wall-clock occurrence of hour:minute in loc that
// is strictly after now. A nil loc means time.Local. The target is built with
// time.Date so a DST change between now and the target is handled by the
// zone rules rather than by adding 24h.
func nextFire(now time.Time, hour, minute int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), hour, minute, 0, 0, loc)
	if !t.After(n) {
		t = time.Date(n.Year(), n.Month(), n.Day()+1, hour, minute, 0, 0, loc)
	}
	return t
}

// newlyToday returns the open tasks that rolled into Today since yesterday:
// scheduled for exactly today's date and created on an earlier UTC date.
// "Today" is the schedule package's UTC-date Today so the digest agrees with
// /today. Tasks with an unparsable CreatedAt are skipped. The result is
// sorted by Position.
func newlyToday(tasks []model.Task, now time.Time) []model.Task {
	todayISO, err := schedule.Parse(schedule.Today, now, "")
	if err != nil {
		return nil
	}
	today, err := time.Parse(schedule.IsoLayout, todayISO)
	if err != nil {
		return nil
	}
	var out []model.Task
	for _, t := range tasks {
		if t.Status != "open" || t.Schedule != todayISO {
			continue
		}
		created, err := time.Parse(time.RFC3339, t.CreatedAt)
		if err != nil {
			continue
		}
		c := created.UTC()
		if !time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC).Before(today) {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}
