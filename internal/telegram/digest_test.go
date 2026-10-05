package telegram

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/store"
)

func TestNextFire(t *testing.T) {
	riga, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	utc := time.UTC

	tests := []struct {
		name         string
		now          time.Time
		hour, minute int
		loc          *time.Location
		want         time.Time
	}{
		{"before the time", time.Date(2026, 10, 5, 7, 0, 0, 0, utc), 8, 0, utc, time.Date(2026, 10, 5, 8, 0, 0, 0, utc)},
		{"after the time", time.Date(2026, 10, 5, 9, 0, 0, 0, utc), 8, 0, utc, time.Date(2026, 10, 6, 8, 0, 0, 0, utc)},
		{"exactly at the time", time.Date(2026, 10, 5, 8, 0, 0, 0, utc), 8, 0, utc, time.Date(2026, 10, 6, 8, 0, 0, 0, utc)},
		{"one nanosecond before", time.Date(2026, 10, 5, 7, 59, 59, 999999999, utc), 8, 0, utc, time.Date(2026, 10, 5, 8, 0, 0, 0, utc)},
		{"non-UTC zone, UTC now past local date", time.Date(2026, 10, 5, 22, 30, 0, 0, utc), 8, 0, riga, time.Date(2026, 10, 6, 8, 0, 0, 0, riga)},
		{"non-UTC zone, still before", time.Date(2026, 10, 5, 4, 0, 0, 0, utc), 8, 0, riga, time.Date(2026, 10, 5, 8, 0, 0, 0, riga)},
		// US DST ends 2026-11-01; the day is 25h long, so +24h would land at 07:00.
		{"DST end day", time.Date(2026, 10, 31, 13, 0, 0, 0, ny), 8, 0, ny, time.Date(2026, 11, 1, 8, 0, 0, 0, ny)},
		{"DST end, next day", time.Date(2026, 11, 1, 9, 0, 0, 0, ny), 8, 0, ny, time.Date(2026, 11, 2, 8, 0, 0, 0, ny)},
		// US DST starts 2026-03-08; the day is 23h long.
		{"DST start day", time.Date(2026, 3, 7, 9, 0, 0, 0, ny), 8, 0, ny, time.Date(2026, 3, 8, 8, 0, 0, 0, ny)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextFire(tc.now, tc.hour, tc.minute, tc.loc)
			if !got.Equal(tc.want) {
				t.Errorf("nextFire = %v, want %v", got, tc.want)
			}
			if got.Hour() != tc.hour || got.Minute() != tc.minute {
				t.Errorf("wall clock = %02d:%02d, want %02d:%02d", got.Hour(), got.Minute(), tc.hour, tc.minute)
			}
		})
	}
}

func TestNextFireDSTGapAndOverlap(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	tests := []struct {
		name         string
		now          time.Time
		hour, minute int
	}{
		{"nonexistent 02:30 on spring-forward day", time.Date(2026, 3, 8, 0, 30, 0, 0, ny), 2, 30},
		{"nonexistent 02:30 day before", time.Date(2026, 3, 7, 12, 0, 0, 0, ny), 2, 30},
		{"ambiguous 01:30 on fall-back day", time.Date(2026, 11, 1, 0, 30, 0, 0, ny), 1, 30},
		{"ambiguous 01:30 after first occurrence", time.Date(2026, 11, 1, 1, 45, 0, 0, ny), 1, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextFire(tc.now, tc.hour, tc.minute, ny)
			if !got.After(tc.now) {
				t.Errorf("nextFire = %v, not after now %v", got, tc.now)
			}
			if got.Sub(tc.now) > 48*time.Hour {
				t.Errorf("nextFire = %v, more than 48h after now %v", got, tc.now)
			}
		})
	}
}

func TestNextFireNilLocationUsesLocal(t *testing.T) {
	now := time.Date(2026, 10, 5, 7, 0, 0, 0, time.Local)
	got := nextFire(now, 8, 0, nil)
	want := time.Date(2026, 10, 5, 8, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("nextFire(nil loc) = %v, want %v", got, want)
	}
}

func digestTask(id, status, sched, created string, pos float64) model.Task {
	return model.Task{ID: id, Title: id, Status: status, Schedule: sched, CreatedAt: created, Position: pos}
}

func TestNewlyToday(t *testing.T) {
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	const today = "2026-10-05"
	yesterday := "2026-10-04T10:00:00Z"
	tests := []struct {
		name string
		task model.Task
		want bool
	}{
		{"created yesterday scheduled today", digestTask("a", "open", today, yesterday, 1), true},
		{"created today", digestTask("a", "open", today, "2026-10-05T00:00:01Z", 1), false},
		{"overdue", digestTask("a", "open", "2026-10-03", yesterday, 1), false},
		{"future", digestTask("a", "open", "2026-10-06", yesterday, 1), false},
		{"done", digestTask("a", "done", today, yesterday, 1), false},
		{"unparsable created", digestTask("a", "open", today, "garbage", 1), false},
		{"empty created", digestTask("a", "open", today, "", 1), false},
		{"created exactly at UTC midnight today", digestTask("a", "open", today, "2026-10-05T00:00:00Z", 1), false},
		{"created one second before UTC midnight", digestTask("a", "open", today, "2026-10-04T23:59:59Z", 1), true},
		{"empty schedule", digestTask("a", "open", "", yesterday, 1), false},
		{"unknown status", digestTask("a", "archived", today, yesterday, 1), false},
		{"non-UTC offset created today UTC", digestTask("a", "open", today, "2026-10-05T01:00:00+03:00", 1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := newlyToday([]model.Task{tc.task}, now)
			if (len(got) == 1) != tc.want {
				t.Errorf("included = %v, want %v", len(got) == 1, tc.want)
			}
		})
	}
}

func TestNewlyTodayOrdersByPosition(t *testing.T) {
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	y := "2026-10-04T10:00:00Z"
	got := newlyToday([]model.Task{
		digestTask("c", "open", "2026-10-05", y, 3000),
		digestTask("a", "open", "2026-10-05", y, 1000),
		digestTask("b", "open", "2026-10-05", y, 2000),
	}, now)
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Errorf("order = %+v", got)
	}
}

func TestNewlyTodayParityWithTodayBucket(t *testing.T) {
	// Today is the UTC date, so the exact ISO date is spelled out per now.
	tests := []struct {
		now      time.Time
		todayISO string
	}{
		{time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC), "2026-10-05"},
		{time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), "2026-10-06"},
		{time.Date(2026, 10, 6, 1, 0, 0, 0, time.FixedZone("x", 3*3600)), "2026-10-05"},
	}
	for _, tc := range tests {
		tasks := []model.Task{
			digestTask("exact", "open", tc.todayISO, "2026-10-01T00:00:00Z", 1),
			digestTask("overdue", "open", "2026-10-01", "2026-10-01T00:00:00Z", 2),
			digestTask("future", "open", "2026-12-01", "2026-10-01T00:00:00Z", 3),
			digestTask("new", "open", tc.todayISO, tc.now.UTC().Format(time.RFC3339), 4),
		}
		if got := newlyToday(tasks, tc.now); len(got) != 1 || got[0].ID != "exact" {
			t.Errorf("now=%v: got %+v, want only exact", tc.now, got)
		}
	}
}

// perUserFailBot fails SendMessage for one chat ID and delegates the rest.
type perUserFailBot struct {
	*fakeBot
	failChat int64
}

func (b *perUserFailBot) SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (int, error) {
	if chatID == b.failChat {
		return 0, errors.New("blocked by user")
	}
	return b.fakeBot.SendMessage(ctx, chatID, html, kb)
}

func seedDigestTask(t *testing.T, s *store.Store, id, created string, pos float64) {
	t.Helper()
	task := model.Task{
		ID: id, Title: "task " + id, Status: "open", Schedule: handlerTestNow.Format("2006-01-02"),
		CreatedAt: created, UpdatedAt: created, Position: pos,
	}
	if err := s.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

const digestYesterday = "2026-05-17T10:00:00Z"

func TestSendDigestEmptySendsNothing(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{1, 2})
	var log bytes.Buffer
	h.SetWriter(&log)
	// created today: excluded
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", handlerTestNow.Format(time.RFC3339), 1000)
	h.sendDigest(context.Background())
	if len(bot.sent) != 0 {
		t.Fatalf("sent=%d want 0", len(bot.sent))
	}
	if !strings.Contains(log.String(), "nothing new") {
		t.Errorf("log missing note: %q", log.String())
	}
}

func TestSendDigestReachesEveryUser(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{1, 2})
	seedDigestTask(t, s, "01JDIGEST0000000000000000B", digestYesterday, 2000)
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	h.sendDigest(context.Background())
	if len(bot.sent) != 6 {
		t.Fatalf("sent=%d want 6: %+v", len(bot.sent), bot.sent)
	}
	for i, uid := range []int64{1, 2} {
		batch := bot.sent[i*3 : i*3+3]
		for _, m := range batch {
			if m.ChatID != uid {
				t.Errorf("chat=%d want %d", m.ChatID, uid)
			}
		}
		if !strings.Contains(batch[0].HTML, "New for Today") || !strings.Contains(batch[0].HTML, "2") {
			t.Errorf("header=%q", batch[0].HTML)
		}
		if !strings.Contains(batch[1].HTML, "task 01JDIGEST0000000000000000A") ||
			!strings.Contains(batch[2].HTML, "task 01JDIGEST0000000000000000B") {
			t.Errorf("rows out of position order: %q, %q", batch[1].HTML, batch[2].HTML)
		}
		if len(batch[1].Keyboard) == 0 {
			t.Errorf("row missing keyboard")
		}
	}
}

func TestSendDigestOneUserFailureDoesNotBlockOthers(t *testing.T) {
	h, _, s, _ := newTestHandler(t, []int64{1, 2})
	fb := &fakeBot{}
	h.bot = &perUserFailBot{fakeBot: fb, failChat: 1}
	var log bytes.Buffer
	h.SetWriter(&log)
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	h.sendDigest(context.Background())
	if len(fb.sent) != 2 {
		t.Fatalf("sent=%d want 2", len(fb.sent))
	}
	for _, m := range fb.sent {
		if m.ChatID != 2 {
			t.Errorf("chat=%d want 2", m.ChatID)
		}
	}
	if !strings.Contains(log.String(), "send header to 1") {
		t.Errorf("failure not logged: %q", log.String())
	}
}

// nthFailBot fails the Nth (1-based) SendMessage to failChat only.
type nthFailBot struct {
	*fakeBot
	failChat int64
	n, seen  int
}

func (b *nthFailBot) SendMessage(ctx context.Context, chatID int64, html string, kb InlineKeyboard) (int, error) {
	if chatID == b.failChat {
		b.seen++
		if b.seen == b.n {
			return 0, errors.New("flood wait")
		}
	}
	return b.fakeBot.SendMessage(ctx, chatID, html, kb)
}

func TestSendDigestRowFailureDoesNotBlockOthers(t *testing.T) {
	h, _, s, _ := newTestHandler(t, []int64{1, 2})
	fb := &fakeBot{}
	// user 1: header ok, first row ok, second row fails.
	h.bot = &nthFailBot{fakeBot: fb, failChat: 1, n: 3}
	var log bytes.Buffer
	h.SetWriter(&log)
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	seedDigestTask(t, s, "01JDIGEST0000000000000000B", digestYesterday, 2000)
	seedDigestTask(t, s, "01JDIGEST0000000000000000C", digestYesterday, 3000)
	h.sendDigest(context.Background())
	var u1, u2 int
	for _, m := range fb.sent {
		switch m.ChatID {
		case 1:
			u1++
		case 2:
			u2++
		}
	}
	if u1 != 2 {
		t.Errorf("user 1 got %d messages, want 2 (header + 1 row, then break)", u1)
	}
	if u2 != 4 {
		t.Errorf("user 2 got %d messages, want 4", u2)
	}
	if !strings.Contains(log.String(), "send row to 1") {
		t.Errorf("failure not logged: %q", log.String())
	}
}

func TestSendDigestListErrorSendsNothing(t *testing.T) {
	h, bot, s, repo := newTestHandler(t, []int64{1})
	var log bytes.Buffer
	h.SetWriter(&log)
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	corrupt := filepath.Join(repo, ".monolog", "tasks", "01JCORRUPT00000000000000Z.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.sendDigest(context.Background())
	if len(bot.sent) != 0 {
		t.Fatalf("sent=%d want 0", len(bot.sent))
	}
	if !strings.Contains(log.String(), "digest: list:") {
		t.Errorf("list error not logged: %q", log.String())
	}
}

func TestSendDigestIgnoresReadOnly(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{1})
	h.SetReadOnly(true)
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	h.sendDigest(context.Background())
	if len(bot.sent) != 2 {
		t.Fatalf("sent=%d want 2", len(bot.sent))
	}
	for _, m := range bot.sent {
		if strings.Contains(m.HTML, "read-only") {
			t.Errorf("unexpected banner: %q", m.HTML)
		}
	}
}

func TestSendDigestFailedPullIsNonFatal(t *testing.T) {
	// TestMain's default pullFunc stub returns an error; the digest must
	// still be served from local state.
	h, bot, s, _ := newTestHandler(t, []int64{1})
	var log bytes.Buffer
	h.SetWriter(&log)
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	h.sendDigest(context.Background())
	if len(bot.sent) != 2 {
		t.Fatalf("sent=%d want 2", len(bot.sent))
	}
	if !strings.Contains(log.String(), "command pull") {
		t.Errorf("pull failure not logged: %q", log.String())
	}
}

// fakeDigestAfter records requested durations and releases each wait on demand.
type fakeDigestAfter struct {
	reqs chan time.Duration
	fire chan time.Time
}

func newFakeDigestAfter(t *testing.T) *fakeDigestAfter {
	f := &fakeDigestAfter{reqs: make(chan time.Duration, 16), fire: make(chan time.Time, 16)}
	old := digestAfter
	digestAfter = func(d time.Duration) <-chan time.Time {
		f.reqs <- d
		return f.fire
	}
	t.Cleanup(func() { digestAfter = old })
	return f
}

func (f *fakeDigestAfter) nextReq(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-f.reqs:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("no digestAfter request")
		return 0
	}
}

func digestTickerCfg() TelegramConfig {
	return TelegramConfig{AllowedUserIDs: []int64{1}, DigestEnabled: true, DigestHour: 12, DigestMinute: 0, DigestLocation: time.UTC}
}

func TestRunDigestTickerFiresAndReschedules(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{1})
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	f := newFakeDigestAfter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	h.cfg = digestTickerCfg()
	go runDigestTicker(ctx, h, done)

	// now is 10:00 UTC, target 12:00 same day.
	if d := f.nextReq(t); d != 2*time.Hour {
		t.Fatalf("first wait=%v want 2h", d)
	}
	f.fire <- time.Now()
	// The clock is frozen at 10:00, so the next target is base=max(now,target)=12:00 -> next day 12:00.
	if d := f.nextReq(t); d != 26*time.Hour {
		t.Fatalf("second wait=%v want 26h", d)
	}
	if len(bot.sent) != 2 {
		t.Fatalf("sent=%d want 2", len(bot.sent))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ticker did not stop on cancel")
	}
}

func TestRunDigestTickerNoDoubleFireWhenClockLags(t *testing.T) {
	// now is 1ms before the target when the timer fires; the next wait must
	// still be for tomorrow, not the same target again.
	target := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	h, _, _, _ := newTestHandlerWithClock(t, []int64{1}, func() time.Time { return target.Add(-time.Millisecond) })
	f := newFakeDigestAfter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	h.cfg = digestTickerCfg()
	go runDigestTicker(ctx, h, done)
	if d := f.nextReq(t); d != time.Millisecond {
		t.Fatalf("first wait=%v want 1ms", d)
	}
	f.fire <- time.Now()
	if d := f.nextReq(t); d < 23*time.Hour {
		t.Fatalf("second wait=%v want ~24h (no double fire)", d)
	}
	cancel()
	<-done
}

func TestRunDigestTickerStopsPromptlyOnCancel(t *testing.T) {
	h, bot, _, _ := newTestHandler(t, []int64{1})
	f := newFakeDigestAfter(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.cfg = digestTickerCfg()
	go runDigestTicker(ctx, h, done)
	f.nextReq(t)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ticker did not stop on cancel")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("sent=%d want 0", len(bot.sent))
	}
}

func TestRunDigestTickerCancelDuringFireSendsNothing(t *testing.T) {
	h, bot, s, _ := newTestHandler(t, []int64{1})
	seedDigestTask(t, s, "01JDIGEST0000000000000000A", digestYesterday, 1000)
	f := newFakeDigestAfter(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.cfg = digestTickerCfg()
	go runDigestTicker(ctx, h, done)
	f.nextReq(t)
	// Cancel and release the timer together; select may pick either case,
	// but the post-wait guard must stop the fire in both.
	cancel()
	f.fire <- time.Now()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ticker did not stop")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("sent=%d want 0", len(bot.sent))
	}
}
