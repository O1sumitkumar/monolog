package telegram

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/schedule"
	"github.com/maksmas/monolog/internal/store"
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

// sendDigest sends the "new for Today" digest to every allow-listed user: a
// header followed by one row per task with the summary keyboard. Nothing is
// sent when no task rolled into Today. Everything is non-fatal and logged to
// the handler's writer. It ignores the readOnly flag because it never writes.
func (h *Handler) sendDigest(ctx context.Context) {
	h.pullBeforeCommand()
	open, err := h.listLocked(store.ListOptions{Status: "open"})
	if err != nil {
		fmt.Fprintf(h.writer, "telegram: digest: list: %v\n", err)
		return
	}
	tasks := newlyToday(open, h.now())
	if len(tasks) == 0 {
		fmt.Fprintln(h.writer, "telegram: digest: nothing new for Today, not sending")
		return
	}
	header := fmt.Sprintf("<b>New for Today</b> — %d", len(tasks))
	for _, uid := range h.cfg.AllowedUserIDs {
		// chatID == userID for private chats.
		if _, err := h.bot.SendMessage(ctx, uid, header, nil); err != nil {
			fmt.Fprintf(h.writer, "telegram: digest: send header to %d: %v\n", uid, err)
			continue
		}
		for _, t := range tasks {
			if _, err := h.bot.SendMessage(ctx, uid, FormatTaskRow(t), BuildSummaryKeyboard(t.ID)); err != nil {
				fmt.Fprintf(h.writer, "telegram: digest: send row to %d: %v\n", uid, err)
				break
			}
		}
	}
}

// digestAfter is the timer seam for runDigestTicker; tests replace it.
var digestAfter = func(d time.Duration) <-chan time.Time { return time.After(d) }

// runDigestTicker fires sendDigest once a day at the configured local time
// until ctx is cancelled. It is stateless: a restart after today's fire time
// waits for tomorrow. After each fire the next target is computed from
// max(now, firedTarget) so a wall clock that lags the timer cannot schedule
// the same target twice. done is closed on exit.
func runDigestTicker(ctx context.Context, h *Handler, cfg TelegramConfig, writer io.Writer, done chan<- struct{}) {
	defer close(done)
	base := h.now()
	for {
		target := nextFire(base, cfg.DigestHour, cfg.DigestMinute, cfg.DigestLocation)
		d := target.Sub(h.now())
		if d < 0 {
			d = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-digestAfter(d):
		}
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(writer, "telegram: digest: firing for %s\n", target.Format(time.RFC3339))
		h.sendDigest(ctx)
		base = h.now()
		if target.After(base) {
			base = target
		}
	}
}
