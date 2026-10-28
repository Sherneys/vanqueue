package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Day is a calendar date counted in days since 1970-01-01. Zero means "no date",
// the same way an empty cell behaves in the original sheet.
type Day int32

func DayOf(t time.Time) Day {
	y, m, d := t.Date()
	return Day(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func Today() Day { return DayOf(time.Now()) }

func (d Day) Time() time.Time { return time.Unix(int64(d)*86400, 0).UTC() }

func (d Day) IsZero() bool { return d == 0 }

// ISO is the value used by <input type="date"> and the database.
func (d Day) ISO() string {
	if d == 0 {
		return ""
	}
	return d.Time().Format("2006-01-02")
}

// String renders d/m/yyyy like the sheet does.
func (d Day) String() string {
	if d == 0 {
		return ""
	}
	t := d.Time()
	return fmt.Sprintf("%d/%d/%d", t.Day(), int(t.Month()), t.Year())
}

func (d Day) MonthStart() Day {
	t := d.Time()
	return DayOf(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC))
}

func (d Day) MonthEnd() Day {
	t := d.Time()
	return DayOf(time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC))
}

// ParseDay accepts yyyy-mm-dd (forms, database) and d/m/yyyy (typed by hand).
func ParseDay(s string) (Day, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	for _, layout := range []string{"2006-01-02", "2/1/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return DayOf(t), nil
		}
	}
	return 0, fmt.Errorf("วันที่ไม่ถูกต้อง: %q", s)
}

// excelSerialDay converts a spreadsheet date serial (days since 1899-12-30).
func excelSerialDay(s string) (Day, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return ParseDay(s)
	}
	return Day(int(f) - 25569), nil
}
