package telegram

import (
	"testing"
	"time"
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
