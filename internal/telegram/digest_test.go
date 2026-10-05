package telegram

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maksmas/monolog/internal/model"
	"github.com/maksmas/monolog/internal/schedule"
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
	for _, now := range []time.Time{
		time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 6, 1, 0, 0, 0, time.FixedZone("x", 3*3600)),
	} {
		todayISO, _ := schedule.Parse(schedule.Today, now, "")
		tasks := []model.Task{
			digestTask("exact", "open", todayISO, "2026-10-01T00:00:00Z", 1),
			digestTask("overdue", "open", "2026-10-01", "2026-10-01T00:00:00Z", 2),
			digestTask("future", "open", "2026-12-01", "2026-10-01T00:00:00Z", 3),
			digestTask("new", "open", todayISO, now.UTC().Format(time.RFC3339), 4),
		}
		inBucket := map[string]bool{}
		for _, tk := range tasks {
			if schedule.MatchesBucket(tk.Schedule, schedule.Today, now) && tk.Schedule == todayISO {
				inBucket[tk.ID] = true
			}
		}
		for _, tk := range newlyToday(tasks, now) {
			if !inBucket[tk.ID] {
				t.Errorf("now=%v: %s in digest but not in /today exact-date set", now, tk.ID)
			}
		}
		if got := newlyToday(tasks, now); len(got) != 1 || got[0].ID != "exact" {
			t.Errorf("now=%v: got %+v, want only exact", now, got)
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
