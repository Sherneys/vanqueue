package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

func day(s string) Day {
	d, err := ParseDay(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Vans 1..5 all ran yesterday in order 4,5,1 (after 2,3 the day before):
// the next day the queue should continue from van 2.
func TestRotationContinuesAfterLastVan(t *testing.T) {
	var vans []Van
	for i := 1; i <= 5; i++ {
		vans = append(vans, Van{Code: fmt.Sprintf("B-%03d", i), Driver: "d", Status: StatusUsable, StartDate: day("2026-10-01")})
	}
	logs := []RunLog{
		{Date: day("2026-10-02"), Code: "B-002", Status: RunDone},
		{Date: day("2026-10-02"), Code: "B-003", Status: RunDone},
		{Date: day("2026-10-03"), Code: "B-004", Status: RunDone},
		{Date: day("2026-10-03"), Code: "B-005", Status: RunDone},
		{Date: day("2026-10-03"), Code: "B-001", Status: RunDone},
	}
	r := Compute(Settings{Date: day("2026-10-04"), Needed: 2}, vans, logs)
	if r.LastCarNum != 1 {
		t.Fatalf("last car = %d, want 1", r.LastCarNum)
	}
	got := []string{r.Queue[0].Code, r.Queue[1].Code, r.Queue[2].Code}
	want := []string{"B-002", "B-003", "B-004"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("queue = %v, want %v", got, want)
	}
	if !r.Queue[1].ShouldRun || r.Queue[2].ShouldRun {
		t.Fatalf("only the first 2 should run")
	}
}

func TestLeaveTakesVanOutOfQueue(t *testing.T) {
	vans := []Van{
		{Code: "B-001", Driver: "a", Status: StatusUsable},
		{Code: "B-002", Driver: "b", Status: StatusUsable, LeaveStart: day("2026-10-03"), LeaveEnd: day("2026-10-05")},
	}
	r := Compute(Settings{Date: day("2026-10-04"), Needed: 2}, vans, nil)
	if r.ReadyCount != 1 || r.ByCode["B-002"].TodayStatus != TodayLeave || r.ByCode["B-002"].Rank != 2 {
		t.Fatalf("unexpected: ready=%d status=%q rank=%d", r.ReadyCount, r.ByCode["B-002"].TodayStatus, r.ByCode["B-002"].Rank)
	}
}

// TestMatchesSheet compares every computed column against the values Google Sheets
// calculated. Set FLEET_XLSX to the downloaded workbook and FLEET_EXPECTED to a CSV
// of its cached values (B,M,N,O,P,Q,R,S,T,U,V,W,X,Y,Z,AA).
func TestMatchesSheet(t *testing.T) {
	xlsx, expected := os.Getenv("FLEET_XLSX"), os.Getenv("FLEET_EXPECTED")
	if xlsx == "" || expected == "" {
		t.Skip("FLEET_XLSX / FLEET_EXPECTED not set")
	}
	st, vans, logs, err := readWorkbook(xlsx)
	if err != nil {
		t.Fatal(err)
	}
	r := Compute(st, vans, logs)
	f, err := os.Open(expected)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs)-1 != r.TotalVans {
		t.Errorf("vans = %d, sheet has %d", r.TotalVans, len(recs)-1)
	}
	intOr := func(n int, ok bool) string {
		if !ok {
			return ""
		}
		return strconv.Itoa(n)
	}
	for _, rec := range recs[1:] {
		v := r.ByCode[rec[0]]
		if v == nil {
			t.Errorf("%s missing", rec[0])
			continue
		}
		rate, _ := strconv.ParseFloat(rec[7], 64)
		checks := []struct {
			col, got, want string
		}{
			{"M", strconv.Itoa(v.MonthTrips), rec[1]},
			{"N", strconv.Itoa(v.WeekTrips), rec[2]},
			{"O", strconv.Itoa(v.TotalTrips), rec[3]},
			{"P", v.LastRun.ISO(), rec[4]},
			{"Q", v.QueueRef.ISO(), rec[5]},
			{"R", strconv.Itoa(v.ServiceDays), rec[6]},
			{"S", fmt.Sprint(math.Abs(v.Rate-rate) < 1e-6), "true"},
			{"T", v.TodayStatus, rec[8]},
			{"U", intOr(v.Key, v.Ready), rec[9]},
			{"V", strconv.Itoa(v.Rank), rec[10]},
			{"W", v.Advice, rec[11]},
			{"X", strconv.Itoa(v.Num), rec[12]},
			{"Y", intOr(v.Rejoin, v.Rejoin >= 0), rec[13]},
			{"Z", intOr(v.Gap, v.Rejoin >= 0), rec[14]},
			{"AA", intOr(v.GroupPos, v.Rejoin == 0), rec[15]},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s %s = %q, sheet %q", rec[0], c.col, c.got, c.want)
			}
		}
	}
	t.Logf("C12=%d ready=%d month trips=%d avg=%.4f spread=%.6f problems=%d longest wait=%d",
		r.LastCarNum, r.ReadyCount, r.MonthTripsTotal, r.AvgTripsPerVan, r.RateSpread, r.ProblemRows, r.LongestWait)
}
