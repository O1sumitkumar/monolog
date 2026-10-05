package telegram

import "time"

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
