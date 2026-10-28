package main

import (
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

//go:embed web
var webFS embed.FS

type server struct {
	store *Store
	auth  *Auth
	pages map[string]*template.Template
	login *template.Template
	mux   *http.ServeMux
}

type page struct {
	Status int
	Title  string
	Nav    string
	Path   string
	User   string
	R      *Report
	Msg    string
	Err    string
	Data   any
}

var (
	thaiWeekdays = [...]string{"อาทิตย์", "จันทร์", "อังคาร", "พุธ", "พฤหัสบดี", "ศุกร์", "เสาร์"}
	thaiMonths   = [...]string{"มกราคม", "กุมภาพันธ์", "มีนาคม", "เมษายน", "พฤษภาคม", "มิถุนายน",
		"กรกฎาคม", "สิงหาคม", "กันยายน", "ตุลาคม", "พฤศจิกายน", "ธันวาคม"}
)

var funcs = template.FuncMap{
	"f1": func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"f2": func(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) },
	"statusClass": func(s string) string {
		switch s {
		case TodayReady:
			return "ok"
		case StatusMaintenance:
			return "bad"
		default:
			return "warn"
		}
	},
	"barHeight": func(n, max int) int {
		if max == 0 {
			return 0
		}
		return n * 160 / max
	},
	"dayNum": func(d Day) int { return d.Time().Day() },
	"add1":   func(i int) int { return i + 1 },
	"isWarn": func(s string) bool { return strings.HasPrefix(s, "⚠") },
	"thaiDate": func(d Day) string {
		t := d.Time()
		return fmt.Sprintf("วัน%sที่ %d %s %d", thaiWeekdays[t.Weekday()], t.Day(), thaiMonths[t.Month()-1], t.Year())
	},
	"thaiMonth": func(d Day) string {
		t := d.Time()
		return fmt.Sprintf("%s %d", thaiMonths[t.Month()-1], t.Year())
	},
}

func newServer(store *Store, auth *Auth) (*server, error) {
	s := &server{store: store, auth: auth, pages: map[string]*template.Template{}, mux: http.NewServeMux()}
	names, err := fs.Glob(webFS, "web/templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if strings.HasSuffix(name, "/layout.html") || strings.HasSuffix(name, "/login.html") {
			continue
		}
		t, err := template.New("").Funcs(funcs).ParseFS(webFS, "web/templates/layout.html", name)
		if err != nil {
			return nil, err
		}
		s.pages[strings.TrimSuffix(name[len("web/templates/"):], ".html")] = t
	}
	if s.login, err = template.New("").Funcs(funcs).ParseFS(webFS, "web/templates/layout.html", "web/templates/login.html"); err != nil {
		return nil, err
	}
	static, _ := fs.Sub(webFS, "web/static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("POST /login", s.loginSubmit)
	s.mux.HandleFunc("POST /logout", s.logout)
	s.mux.HandleFunc("GET /{$}", s.dashboard)
	s.mux.HandleFunc("GET /queue", s.queue)
	s.mux.HandleFunc("POST /queue/record", s.queueRecord)
	s.mux.HandleFunc("GET /vans", s.vans)
	s.mux.HandleFunc("GET /vans/new", s.vanForm)
	s.mux.HandleFunc("GET /vans/{id}", s.vanForm)
	s.mux.HandleFunc("POST /vans", s.vanSave)
	s.mux.HandleFunc("POST /vans/{id}", s.vanSave)
	s.mux.HandleFunc("POST /vans/{id}/delete", s.vanDelete)
	s.mux.HandleFunc("GET /logs", s.logs)
	s.mux.HandleFunc("POST /logs", s.logsAdd)
	s.mux.HandleFunc("GET /logs/{id}", s.logForm)
	s.mux.HandleFunc("POST /logs/{id}", s.logSave)
	s.mux.HandleFunc("POST /logs/{id}/delete", s.logDelete)
	s.mux.HandleFunc("GET /settings", s.settings)
	s.mux.HandleFunc("POST /settings", s.settingsSave)
	s.mux.HandleFunc("POST /date", s.setDate)
	s.mux.HandleFunc("GET /guide", s.guide)
	s.mux.HandleFunc("GET /export/vans.csv", s.exportVans)
	s.mux.HandleFunc("GET /export/logs.csv", s.exportLogs)
	return s, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.guard(s.mux).ServeHTTP(w, r) }

func (s *server) report() (*Report, error) {
	st, err := s.store.Settings()
	if err != nil {
		return nil, err
	}
	vans, err := s.store.Vans()
	if err != nil {
		return nil, err
	}
	logs, err := s.store.Logs()
	if err != nil {
		return nil, err
	}
	return Compute(st, vans, logs), nil
}

// render loads a fresh report when p.R is nil and writes the page.
func (s *server) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	if p.R == nil {
		rep, err := s.report()
		if err != nil {
			s.fail(w, err)
			return
		}
		p.R = rep
	}
	// Path is where forms on the page send the user back to; drop the one-off flash
	// message so it doesn't reappear after the next action.
	back := *r.URL
	q := back.Query()
	q.Del("msg")
	back.RawQuery = q.Encode()
	p.Path = back.RequestURI()
	p.User = currentUser(r)
	if p.Msg == "" {
		p.Msg = r.URL.Query().Get("msg")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if p.Status != 0 {
		w.WriteHeader(p.Status)
	}
	if err := s.pages[name].ExecuteTemplate(w, "layout", p); err != nil {
		// A browser that navigates away mid-response is normal, not an error.
		if !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) {
			log.Printf("render %s: %v", name, err)
		}
	}
}

func (s *server) fail(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "เกิดข้อผิดพลาด: "+err.Error(), http.StatusInternalServerError)
}

func redirect(w http.ResponseWriter, r *http.Request, to, msg string) {
	if msg != "" {
		sep := "?"
		if strings.Contains(to, "?") {
			sep = "&"
		}
		to += sep + "msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// ---- Dashboard ----

func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	ready := rep.Queue[:rep.ReadyCount]
	first := ready[:min(5, len(ready))]
	var last []*VanRow
	for i := len(ready) - 1; i >= 0 && len(last) < 5; i-- {
		last = append(last, ready[i])
	}
	chartMax := 0
	for _, d := range rep.Daily {
		chartMax = max(chartMax, d.Count)
	}
	var next *VanRow
	if len(ready) > 0 {
		next = ready[0]
	}
	s.render(w, r, "dashboard", page{Title: "ภาพรวม", Nav: "dashboard", R: rep, Data: map[string]any{
		"First": first, "Last": last, "ChartMax": chartMax, "Next": next,
	}})
}

// ---- Queue (แนะนำรถวิ่งวันนี้) ----

func (s *server) queue(w http.ResponseWriter, r *http.Request) {
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	logged := map[string]bool{}
	for _, l := range rep.Logs {
		if l.Date == rep.Date {
			logged[NormCode(l.Code)] = true
		}
	}
	s.render(w, r, "queue", page{Title: "คิวรถวันนี้", Nav: "queue", R: rep, Data: map[string]any{"Logged": logged}})
}

// queueRecord writes "วิ่งแล้ว" rows for the ticked vans on the selected date.
func (s *server) queueRecord(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	logged := map[string]bool{}
	for _, l := range rep.Logs {
		if l.Date == rep.Date {
			logged[NormCode(l.Code)] = true
		}
	}
	var add []RunLog
	for _, code := range r.Form["code"] {
		code = NormCode(code)
		if rep.ByCode[code] == nil || logged[code] {
			continue
		}
		logged[code] = true
		add = append(add, RunLog{Date: rep.Date, Code: code, Status: RunDone, Destination: r.FormValue("destination")})
	}
	if err := s.store.AddLogs(add); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/queue", fmt.Sprintf("บันทึกว่าวิ่งแล้ว %d คัน วันที่ %s", len(add), rep.Date))
}

// ---- Vans (รายชื่อรถตู้) ----

func (s *server) vans(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "vans", page{Title: "รายชื่อรถตู้", Nav: "vans"})
}

type vanFormData struct {
	Van      Van
	Row      *VanRow
	Statuses []string
}

func (s *server) vanForm(w http.ResponseWriter, r *http.Request) {
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	d := vanFormData{Van: Van{Status: StatusUsable, StartDate: rep.Date}, Statuses: VanStatuses}
	title := "เพิ่มรถตู้"
	if id := pathID(r); id != 0 {
		v, err := s.store.Van(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		d.Van, d.Row, title = v, rep.ByCode[NormCode(v.Code)], "แก้ไข "+v.Code
	}
	s.render(w, r, "van_form", page{Title: title, Nav: "vans", R: rep, Data: d})
}

func (s *server) vanSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	v := Van{
		ID: pathID(r), Code: NormCode(r.FormValue("code")), Driver: strings.TrimSpace(r.FormValue("driver")),
		Plate: strings.TrimSpace(r.FormValue("plate")), Phone: strings.TrimSpace(r.FormValue("phone")),
		Note: strings.TrimSpace(r.FormValue("note")), Zone: strings.TrimSpace(r.FormValue("zone")),
		Status: r.FormValue("status"),
	}
	var errs []string
	if v.Code == "" {
		errs = append(errs, "กรุณากรอกรหัสรถตู้")
	} else if codeNumber(v.Code) == 0 {
		errs = append(errs, "รหัสรถตู้ต้องเป็นรูปแบบ B-001 (ตัวเลขใช้จัดคิว)")
	}
	if v.Driver == "" {
		errs = append(errs, "กรุณากรอกชื่อคนขับ")
	}
	if !slices.Contains(VanStatuses, v.Status) {
		errs = append(errs, "สถานะรถไม่ถูกต้อง")
	}
	if seats := strings.TrimSpace(r.FormValue("seats")); seats != "" {
		n, err := strconv.Atoi(seats)
		if err != nil || n < 0 {
			errs = append(errs, "จำนวนที่นั่งต้องเป็นตัวเลข")
		}
		v.Seats = n
	}
	for _, f := range []struct {
		dst  *Day
		name string
	}{{&v.StartDate, "start_date"}, {&v.LeaveStart, "leave_start"}, {&v.LeaveEnd, "leave_end"}} {
		d, err := ParseDay(r.FormValue(f.name))
		if err != nil {
			errs = append(errs, err.Error())
		}
		*f.dst = d
	}
	if (v.LeaveStart == 0) != (v.LeaveEnd == 0) {
		errs = append(errs, "กรอกวันเริ่มลาและวันสิ้นสุดลาให้ครบทั้งสองช่อง")
	} else if v.LeaveStart != 0 && v.LeaveEnd < v.LeaveStart {
		errs = append(errs, "วันสิ้นสุดลาต้องไม่ก่อนวันเริ่มลา")
	}
	if len(errs) == 0 {
		if err := s.store.SaveVan(&v); err != nil {
			if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
				errs = append(errs, "รหัส "+v.Code+" มีอยู่แล้ว")
			} else {
				s.fail(w, err)
				return
			}
		}
	}
	if len(errs) > 0 {
		title := "เพิ่มรถตู้"
		if v.ID != 0 {
			title = "แก้ไข " + v.Code
		}
		s.render(w, r, "van_form", page{Status: http.StatusUnprocessableEntity, Title: title, Nav: "vans", Err: strings.Join(errs, " · "),
			Data: vanFormData{Van: v, Statuses: VanStatuses}})
		return
	}
	redirect(w, r, "/vans", "บันทึก "+v.Code+" แล้ว")
}

func (s *server) vanDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteVan(pathID(r)); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/vans", "ลบรถตู้แล้ว")
}

// ---- Run log (บันทึกการวิ่งประจำวัน) ----

const logsPerPage = 200

func (s *server) logs(w http.ResponseWriter, r *http.Request) {
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	view := q.Get("date") // "" = settings date, "all", or yyyy-mm-dd
	day := rep.Date
	if view != "" && view != "all" {
		if d, err := ParseDay(view); err == nil && d != 0 {
			day = d
		}
	}
	problems := q.Get("problems") == "1"
	var rows []LogRow
	for i := len(rep.Logs) - 1; i >= 0; i-- { // newest first
		l := rep.Logs[i]
		if view != "all" && l.Date != day {
			continue
		}
		if problems && l.Check == "" {
			continue
		}
		rows = append(rows, l)
	}
	total := len(rows)
	pg, _ := strconv.Atoi(q.Get("page"))
	pg = max(pg, 1)
	pages := max(1, (total+logsPerPage-1)/logsPerPage)
	pg = min(pg, pages)
	rows = rows[(pg-1)*logsPerPage : min(pg*logsPerPage, total)]

	s.render(w, r, "logs", page{Title: "บันทึกการวิ่งประจำวัน", Nav: "logs", R: rep, Data: map[string]any{
		"Rows": rows, "Total": total, "All": view == "all", "Day": day, "Problems": problems,
		"Page": pg, "Pages": pages, "Statuses": RunStatuses,
		"PrevPage": pg - 1, "NextPage": pg + 1, "ViewParam": view,
	}})
}

func (s *server) logsAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	d, err := ParseDay(r.FormValue("date"))
	back := "/logs?date=" + r.FormValue("date")
	if err != nil || d == 0 {
		redirect(w, r, back, "⚠ กรุณาเลือกวันที่")
		return
	}
	status := r.FormValue("status")
	if !slices.Contains(RunStatuses, status) {
		status = RunDone
	}
	codes := strings.FieldsFunc(r.FormValue("codes"), func(c rune) bool {
		return c == ',' || c == ' ' || c == '\n' || c == '\r' || c == '\t'
	})
	// A van runs at most once a day (the ตรวจสอบ column flags repeats), so skip
	// codes typed twice or already logged on that date.
	seen := map[string]bool{}
	for _, l := range rep.Logs {
		if l.Date == d {
			seen[NormCode(l.Code)] = true
		}
	}
	var add []RunLog
	var unknown, skipped []string
	for _, c := range codes {
		c = NormCode(c)
		if c == "" {
			continue
		}
		if rep.ByCode[c] == nil {
			unknown = append(unknown, c)
			continue
		}
		if seen[c] {
			skipped = append(skipped, c)
			continue
		}
		seen[c] = true
		add = append(add, RunLog{Date: d, Code: c, Status: status,
			Destination: strings.TrimSpace(r.FormValue("destination")), Note: strings.TrimSpace(r.FormValue("note"))})
	}
	if len(unknown) > 0 {
		redirect(w, r, back, "⚠ ไม่พบรหัส "+strings.Join(unknown, ", ")+" — ยังไม่ได้บันทึก")
		return
	}
	var skipMsg string
	if len(skipped) > 0 {
		skipMsg = " · ข้าม " + strings.Join(skipped, ", ") + " (มีบันทึกวันนี้แล้ว)"
	}
	if len(add) == 0 {
		if skipMsg != "" {
			redirect(w, r, back, "⚠ ไม่ได้เพิ่มแถวใหม่"+skipMsg)
		} else {
			redirect(w, r, back, "⚠ กรุณาใส่รหัสรถตู้")
		}
		return
	}
	if err := s.store.AddLogs(add); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, back, fmt.Sprintf("เพิ่ม %d แถวแล้ว", len(add))+skipMsg)
}

func (s *server) logForm(w http.ResponseWriter, r *http.Request) {
	l, err := s.store.Log(pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "log_form", page{Title: "แก้ไขบันทึกการวิ่ง", Nav: "logs", Data: map[string]any{"Log": l, "Statuses": RunStatuses}})
}

func (s *server) logSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	d, err := ParseDay(r.FormValue("date"))
	l := RunLog{ID: pathID(r), Date: d, Code: NormCode(r.FormValue("code")), Status: r.FormValue("status"),
		Destination: strings.TrimSpace(r.FormValue("destination")), Note: strings.TrimSpace(r.FormValue("note"))}
	if err != nil || d == 0 || l.Code == "" || !slices.Contains(RunStatuses, l.Status) {
		s.render(w, r, "log_form", page{Status: http.StatusUnprocessableEntity, Title: "แก้ไขบันทึกการวิ่ง", Nav: "logs", Err: "กรุณากรอกวันที่ รหัสรถ และสถานะให้ถูกต้อง",
			Data: map[string]any{"Log": l, "Statuses": RunStatuses}})
		return
	}
	if err := s.store.SaveLog(l); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/logs?date="+d.ISO(), "บันทึกแล้ว")
}

func (s *server) logDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteLog(pathID(r)); err != nil {
		s.fail(w, err)
		return
	}
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/logs") {
		back = "/logs"
	}
	redirect(w, r, back, "ลบแถวแล้ว")
}

// ---- Settings ----

func (s *server) settings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings", page{Title: "ตั้งค่า", Nav: "settings"})
}

func (s *server) settingsSave(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Settings()
	if err != nil {
		s.fail(w, err)
		return
	}
	if d, err := ParseDay(r.FormValue("date")); err == nil && d != 0 {
		st.Date = d
	}
	if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("needed"))); err == nil && n >= 0 {
		st.Needed = n
	}
	if err := s.store.SaveSettings(st); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/settings", "บันทึกการตั้งค่าแล้ว")
}

// setDate changes the planning date from the header bar.
func (s *server) setDate(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Settings()
	if err != nil {
		s.fail(w, err)
		return
	}
	switch r.FormValue("move") {
	case "prev":
		st.Date--
	case "next":
		st.Date++
	case "today":
		st.Date = Today()
	default:
		if d, err := ParseDay(r.FormValue("date")); err == nil && d != 0 {
			st.Date = d
		}
	}
	if err := s.store.SaveSettings(st); err != nil {
		s.fail(w, err)
		return
	}
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/"
	}
	// A dated log view should follow the new date.
	if strings.HasPrefix(back, "/logs") && !strings.Contains(back, "date=all") {
		back = "/logs"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *server) guide(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "guide", page{Title: "วิธีใช้", Nav: "guide"})
}

// ---- CSV export ----

func (s *server) exportVans(w http.ResponseWriter, r *http.Request) {
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	cw := csvResponse(w, "vans-"+rep.Date.ISO()+".csv")
	cw.Write([]string{"ลำดับ", "รหัสรถตู้", "ชื่อคนขับ", "ทะเบียนรถ", "เบอร์โทร", "จำนวนที่นั่ง", "หมายเหตุ", "โซนที่พัก", "สถานะรถ",
		"วันที่เริ่มวิ่ง", "วันเริ่มลา", "วันสิ้นสุดลา", "เที่ยววิ่งเดือนนี้", "เที่ยววิ่ง 7 วันล่าสุด", "เที่ยววิ่งสะสม", "วิ่งล่าสุด",
		"วันอ้างอิงคิว", "วันให้บริการเดือนนี้", "อัตราเฉลี่ยเที่ยว/วัน", "สถานะวันนี้", "ลำดับคิว", "คำแนะนำวันนี้"})
	for i, v := range rep.Vans {
		cw.Write(csvCells(strconv.Itoa(i+1), v.Code, v.Driver, v.Plate, v.Phone, strconv.Itoa(v.Seats), v.Note, v.Zone, v.Status,
			v.StartDate.String(), v.LeaveStart.String(), v.LeaveEnd.String(), strconv.Itoa(v.MonthTrips), strconv.Itoa(v.WeekTrips),
			strconv.Itoa(v.TotalTrips), v.LastRun.String(), v.QueueRef.String(), strconv.Itoa(v.ServiceDays),
			strconv.FormatFloat(v.Rate, 'f', 2, 64), v.TodayStatus, strconv.Itoa(v.Rank), v.Advice))
	}
	cw.Flush()
}

func (s *server) exportLogs(w http.ResponseWriter, r *http.Request) {
	rep, err := s.report()
	if err != nil {
		s.fail(w, err)
		return
	}
	cw := csvResponse(w, "run-log.csv")
	cw.Write([]string{"วันที่", "รหัสรถตู้", "ชื่อคนขับ", "สถานที่ไป", "สถานะ", "หมายเหตุ", "ตรวจสอบ"})
	for _, l := range rep.Logs {
		cw.Write(csvCells(l.Date.String(), l.Code, l.Driver, l.Destination, l.Status, l.Note, l.Check))
	}
	cw.Flush()
}

// csvCells stops Excel from running cell text as a formula (e.g. a note starting with "=").
func csvCells(cells ...string) []string {
	for i, c := range cells {
		if c != "" && strings.ContainsRune("=+@\t\r", rune(c[0])) {
			cells[i] = "'" + c
		}
	}
	return cells
}

func csvResponse(w http.ResponseWriter, name string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write([]byte("\xEF\xBB\xBF")) // BOM so Excel reads Thai correctly
	return csv.NewWriter(w)
}
