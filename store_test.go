package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// These tests need a disposable Postgres database; they wipe its tables.
// Never point FLEET_TEST_DATABASE_URL at the real Supabase project.
func testStore(t *testing.T) *Store {
	dsn := os.Getenv("FLEET_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLEET_TEST_DATABASE_URL not set")
	}
	st, err := OpenStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	vans := []Van{
		{Code: "B-001", Driver: "a", Status: StatusUsable, StartDate: day("2026-10-01")},
		{Code: "B-02", Driver: "b", Status: StatusUsable},
	}
	logs := []RunLog{
		{Date: day("2026-10-02"), Code: "B-02", Status: RunDone},
		{Date: day("2026-10-03"), Code: "B-02", Status: RunDone},
		{Date: day("2026-10-03"), Code: "B-001", Status: RunDone},
		{Code: "B-001", Status: RunDone}, // no date, like an unfinished sheet row
	}
	if err := st.ReplaceAll(Settings{Date: day("2026-10-04"), Needed: 1}, vans, logs); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStoreRoundTrip(t *testing.T) {
	st := testStore(t)
	set, _ := st.Settings()
	vans, _ := st.Vans()
	logs, _ := st.Logs()
	if set.Date != day("2026-10-04") || set.Needed != 1 || len(vans) != 2 || len(logs) != 4 {
		t.Fatalf("settings %+v, %d vans, %d logs", set, len(vans), len(logs))
	}
	if vans[0].Code != "B-001" || vans[0].StartDate != day("2026-10-01") || vans[1].StartDate != 0 {
		t.Fatalf("vans: %+v", vans)
	}
	if logs[0].Date != 0 {
		t.Fatalf("undated log should sort first and stay undated: %+v", logs[0])
	}
}

func TestRenameVanKeepsHistory(t *testing.T) {
	st := testStore(t)
	vans, _ := st.Vans()
	v := vans[1]
	v.Code = "B-002"
	if err := st.SaveVan(&v); err != nil {
		t.Fatal(err)
	}
	logs, _ := st.Logs()
	set, _ := st.Settings()
	vans, _ = st.Vans()
	r := Compute(set, vans, logs)
	if r.ProblemRows != 1 { // only the undated row
		t.Fatalf("problem rows = %d", r.ProblemRows)
	}
	if got := r.ByCode["B-002"]; got == nil || got.TotalTrips != 2 {
		t.Fatalf("renamed van lost its trips: %+v", got)
	}
}

func TestAddLogsSkipsDuplicates(t *testing.T) {
	st := testStore(t)
	s, err := newServer(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	// B-001 already ran on 3/10; B-02 is typed twice.
	req := postForm("/logs", "date=2026-10-03&status="+url.QueryEscape(RunDone)+"&codes="+url.QueryEscape("B-001 b-02, B-02"))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	loc, _ := url.QueryUnescape(rec.Header().Get("Location"))
	if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "ข้าม B-001, B-02, B-02") {
		t.Fatalf("got %d %q", rec.Code, loc)
	}
	logs, _ := st.Logs()
	if len(logs) != 4 {
		t.Fatalf("expected no new rows, have %d", len(logs))
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, postForm("/logs", "date=2026-10-04&codes="+url.QueryEscape("B-001 B-001")))
	logs, _ = st.Logs()
	if len(logs) != 5 {
		t.Fatalf("expected one new row, have %d", len(logs))
	}
}

func TestFormsDropFlashMessage(t *testing.T) {
	st := testStore(t)
	s, err := newServer(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/queue?msg=hello", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `name="back" value="/queue"`) || !strings.Contains(body, ">hello<") {
		t.Fatalf("back link should drop msg but the page should still show it")
	}
}

func TestCSVCells(t *testing.T) {
	got := csvCells("=1+1", "+66", "@x", "-ok", "ปกติ", "")
	want := []string{"'=1+1", "'+66", "'@x", "-ok", "ปกติ", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("csvCells[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
