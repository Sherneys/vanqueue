package main

import (
	"sort"
	"strconv"
	"strings"
)

// The engine is a port of the formulas in "ระบบจัดสรรรถตู้200คัน" (R6 — คิววนตามหมายเลขรถ).
// Comments name the sheet column each value comes from.

const (
	StatusUsable      = "ใช้งานได้"
	StatusMaintenance = "ซ่อมบำรุง"
	TodayReady        = "พร้อมวิ่ง"
	TodayNotStarted   = "ยังไม่ถึงวันเริ่มวิ่ง"
	TodayLeave        = "ลา"
	RunDone           = "วิ่งแล้ว"
	AdviceRun         = "ควรวิ่ง"
	AdviceSkip        = "ไม่ควรวิ่ง"
)

var VanStatuses = []string{"ใช้งานได้", "ซ่อมบำรุง", "ลาหยุด", "เลิกวิ่ง", "พักงาน", "รอคิว"}
var RunStatuses = []string{"วิ่งแล้ว", "ยกเลิก", "โอเวอร์ไนท์"}

// NormCode is UPPER(TRIM(code)), the sheet's "รหัส (ระบบ)" column.
func NormCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// codeNumber is VALUE(MID(code,3,5)): "B-001" -> 1.
func codeNumber(code string) int {
	r := []rune(code)
	if len(r) < 3 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(r[2:min(len(r), 7)])))
	if err != nil {
		return 0
	}
	return n
}

// mod matches the spreadsheet MOD: the result takes the divisor's sign.
func mod(a, b int) int {
	if b == 0 {
		return 0
	}
	m := a % b
	if m != 0 && (m < 0) != (b < 0) {
		m += b
	}
	return m
}

type VanRow struct {
	Van
	Num         int     // X หมายเลขรถ
	MonthTrips  int     // M เที่ยววิ่งเดือนนี้ (ถึงเมื่อวาน)
	WeekTrips   int     // N เที่ยววิ่ง 7 วันล่าสุด
	TotalTrips  int     // O เที่ยววิ่งสะสมทั้งหมด
	LastRun     Day     // P วิ่งล่าสุดวันที่
	QueueRef    Day     // Q วันอ้างอิงคิว
	ServiceDays int     // R จำนวนวันให้บริการเดือนนี้
	Rate        float64 // S อัตราเฉลี่ยเที่ยว/วัน
	TodayStatus string  // T สถานะวันนี้
	Ready       bool
	Rejoin      int // Y: -1 not ready, 0 continues the rotation, 1 new / back from leave
	Gap         int // Z ช่วงห่าง / รถคันถัดไป
	GroupPos    int // AA ตำแหน่งในกลุ่ม (valid when Rejoin == 0)
	Key         int // U คีย์คิว (ready vans only)
	Rank        int // V ลำดับคิว
	Advice      string
	ShouldRun   bool
	WaitDays    int // แนะนำรถวิ่งวันนี้ column I
	HasWait     bool
}

type LogRow struct {
	RunLog
	Driver string // ชื่อคนขับ (อัตโนมัติ)
	Check  string // ตรวจสอบ
}

type DailyCount struct {
	Day   Day
	Count int
}

type Report struct {
	Settings
	MonthStart  Day
	Vans        []*VanRow // list order
	Queue       []*VanRow // by Rank
	ByCode      map[string]*VanRow
	TotalVans   int
	MaxNum      int
	ReadyCount  int // ตั้งค่า C9
	Recommended int // ตั้งค่า C10
	LastCarNum  int // ตั้งค่า C12
	Warning     string

	MonthTripsTotal  int
	AvgTripsPerVan   float64
	RateSpread       float64
	ReadyNoTrips     int
	Maintenance      int
	OtherUnavailable int
	ReadySkip        int
	LongestWait      int
	Daily            []DailyCount
	Logs             []LogRow
	ProblemRows      int
}

func Compute(st Settings, vans []Van, logs []RunLog) *Report {
	date := st.Date
	ms := date.MonthStart()
	r := &Report{Settings: st, MonthStart: ms, ByCode: map[string]*VanRow{}}

	type agg struct {
		month, week, total int
		last               Day
	}
	stats := map[string]*agg{}
	for _, l := range logs {
		if l.Status != RunDone || l.Date == 0 || l.Date >= date {
			continue
		}
		code := NormCode(l.Code)
		a := stats[code]
		if a == nil {
			a = &agg{}
			stats[code] = a
		}
		a.total++
		if l.Date >= ms {
			a.month++
		}
		if l.Date >= date-7 {
			a.week++
		}
		a.last = max(a.last, l.Date)
	}

	for _, v := range vans {
		row := &VanRow{Van: v, Num: codeNumber(v.Code), Rejoin: -1}
		if a := stats[NormCode(v.Code)]; a != nil {
			row.MonthTrips, row.WeekTrips, row.TotalTrips, row.LastRun = a.month, a.week, a.total, a.last
		}
		hasLeave := v.LeaveStart != 0 && v.LeaveEnd != 0

		// R: days in service this month up to yesterday, minus leave days.
		base := ms
		if v.StartDate != 0 {
			base = max(v.StartDate, ms)
		}
		days := max(0, int(date-base))
		leave := 0
		if hasLeave {
			leave = max(0, int(min(v.LeaveEnd, date-1)-max(v.LeaveStart, base))+1)
		}
		row.ServiceDays = max(0, days-leave)
		if row.ServiceDays > 0 {
			row.Rate = float64(row.MonthTrips) / float64(row.ServiceDays)
		}

		// T
		switch {
		case v.Status != StatusUsable:
			row.TodayStatus = v.Status
		case v.StartDate != 0 && v.StartDate > date:
			row.TodayStatus = TodayNotStarted
		case hasLeave && date >= v.LeaveStart && date <= v.LeaveEnd:
			row.TodayStatus = TodayLeave
		default:
			row.TodayStatus = TodayReady
		}
		row.Ready = row.TodayStatus == TodayReady

		// Y
		if row.Ready {
			row.Rejoin = 0
			if row.LastRun == 0 || row.LastRun < v.StartDate ||
				(hasLeave && v.LeaveEnd < date && row.LastRun <= v.LeaveEnd) {
				row.Rejoin = 1
			}
		}

		r.Vans = append(r.Vans, row)
		r.ByCode[NormCode(v.Code)] = row
		r.MaxNum = max(r.MaxNum, row.Num)
		if row.Ready {
			r.ReadyCount++
		}
	}
	r.TotalVans = len(r.Vans)
	maxNum := r.MaxNum

	// Z: distance back to the previous continuing van that last ran on the same day
	// (wrapping around), or for rejoining vans, the next continuing van by number.
	for _, row := range r.Vans {
		switch row.Rejoin {
		case 0:
			prev, groupMax := 0, 0
			for _, o := range r.Vans {
				if o.Rejoin != 0 || o.LastRun != row.LastRun {
					continue
				}
				groupMax = max(groupMax, o.Num)
				if o.Num < row.Num {
					prev = max(prev, o.Num)
				}
			}
			if prev > 0 {
				row.Gap = row.Num - prev
			} else {
				row.Gap = row.Num + maxNum - groupMax
			}
		case 1:
			next, first := 0, 0
			for _, o := range r.Vans {
				if o.Rejoin != 0 {
					continue
				}
				if o.Num > row.Num && (next == 0 || o.Num < next) {
					next = o.Num
				}
				if first == 0 || o.Num < first {
					first = o.Num
				}
			}
			if next > 0 {
				row.Gap = next
			} else {
				row.Gap = first
			}
		}
	}

	// AA: position inside the group of vans that last ran on the same day. The group
	// starts right after its largest gap.
	type group struct{ maxGap, start int }
	groups := map[Day]*group{}
	for _, row := range r.Vans {
		if row.Rejoin != 0 {
			continue
		}
		g := groups[row.LastRun]
		if g == nil {
			g = &group{maxGap: row.Gap, start: row.Num}
			groups[row.LastRun] = g
		} else if row.Gap > g.maxGap || (row.Gap == g.maxGap && row.Num < g.start) {
			g.maxGap, g.start = row.Gap, row.Num
		}
	}
	for _, row := range r.Vans {
		if row.Rejoin == 0 {
			row.GroupPos = mod(row.Num-groups[row.LastRun].start, maxNum)
		}
	}

	// Q
	byNum := map[int]*VanRow{}
	for _, row := range r.Vans {
		if _, ok := byNum[row.Num]; !ok {
			byNum[row.Num] = row
		}
	}
	for _, row := range r.Vans {
		switch {
		case row.Rejoin == 0:
			row.QueueRef = row.LastRun
		case row.Rejoin == 1 && row.Gap == 0:
			row.QueueRef = row.StartDate
		case row.Rejoin == 1:
			if o := byNum[row.Gap]; o != nil {
				row.QueueRef = o.LastRun
			}
		}
	}

	// ตั้งค่า C12: the last van (by group position) of the most recent day's run.
	var latest Day
	for _, row := range r.Vans {
		latest = max(latest, row.LastRun)
	}
	lastPos := 0
	for _, row := range r.Vans {
		if row.LastRun == latest && row.Rejoin == 0 {
			lastPos = max(lastPos, row.GroupPos)
		}
	}
	for _, row := range r.Vans {
		if row.LastRun == latest && row.Rejoin == 0 && row.GroupPos == lastPos &&
			(r.LastCarNum == 0 || row.Num < r.LastCarNum) {
			r.LastCarNum = row.Num
		}
	}

	// U, V, W: rotate by van number starting after C12; unavailable vans go last.
	for _, row := range r.Vans {
		if row.Ready {
			row.Key = mod(row.Num-r.LastCarNum-1, maxNum) + 1
		}
	}
	notReadySeen := 0
	r.Recommended = min(st.Needed, r.ReadyCount)
	for i, row := range r.Vans {
		if row.Ready {
			rank := 1
			for j, o := range r.Vans {
				if o.Ready && (o.Key < row.Key || (o.Key == row.Key && j < i)) {
					rank++
				}
			}
			row.Rank = rank
			row.ShouldRun = rank <= st.Needed
			row.Advice = AdviceSkip
			if row.ShouldRun {
				row.Advice = AdviceRun
			}
		} else {
			notReadySeen++
			row.Rank = r.ReadyCount + notReadySeen
			row.Advice = row.TodayStatus + " - " + AdviceSkip
		}
		if row.QueueRef != 0 && row.QueueRef <= date {
			row.WaitDays, row.HasWait = int(date-row.QueueRef), true
		}
	}
	r.Queue = append([]*VanRow(nil), r.Vans...)
	sort.SliceStable(r.Queue, func(i, j int) bool { return r.Queue[i].Rank < r.Queue[j].Rank })

	if r.ReadyCount < st.Needed {
		r.Warning = "⚠ รถพร้อมวิ่งมีแค่ " + strconv.Itoa(r.ReadyCount) + " คัน น้อยกว่าที่ต้องการ " + strconv.Itoa(st.Needed) + " คัน"
	}

	// Dashboard
	minRate, maxRate, haveRate := 0.0, 0.0, false
	var oldestRef Day
	for _, row := range r.Vans {
		r.MonthTripsTotal += row.MonthTrips
		if row.TodayStatus == StatusMaintenance {
			r.Maintenance++
		}
		if !row.Ready {
			continue
		}
		if row.MonthTrips == 0 {
			r.ReadyNoTrips++
		}
		if !haveRate || row.Rate < minRate {
			minRate = row.Rate
		}
		if !haveRate || row.Rate > maxRate {
			maxRate = row.Rate
		}
		haveRate = true
		if row.QueueRef != 0 && (oldestRef == 0 || row.QueueRef < oldestRef) {
			oldestRef = row.QueueRef
		}
	}
	if r.TotalVans > 0 {
		r.AvgTripsPerVan = float64(r.MonthTripsTotal) / float64(r.TotalVans)
	}
	r.RateSpread = maxRate - minRate
	r.OtherUnavailable = r.TotalVans - r.ReadyCount - r.Maintenance
	r.ReadySkip = r.ReadyCount - r.Recommended
	if oldestRef != 0 {
		r.LongestWait = int(date - oldestRef)
	}

	// ข้อมูลกราฟ: completed runs per day of the selected month.
	perDay := map[Day]int{}
	for _, l := range logs {
		if l.Status == RunDone {
			perDay[l.Date]++
		}
	}
	for d := ms; d <= date.MonthEnd(); d++ {
		r.Daily = append(r.Daily, DailyCount{Day: d, Count: perDay[d]})
	}

	// บันทึกการวิ่งประจำวัน: driver lookup and the ตรวจสอบ column.
	type dayCode struct {
		d    Day
		code string
	}
	dup := map[dayCode]int{}
	for _, l := range logs {
		dup[dayCode{l.Date, NormCode(l.Code)}]++
	}
	r.Logs = make([]LogRow, len(logs))
	for i, l := range logs {
		lr := LogRow{RunLog: l}
		code := NormCode(l.Code)
		van := r.ByCode[code]
		if van != nil {
			lr.Driver = van.Driver
		} else if code != "" {
			lr.Driver = "⚠ ไม่พบรหัส"
		}
		switch {
		case code == "":
		case l.Date == 0:
			lr.Check = "ไม่มีวันที่"
		case van == nil:
			lr.Check = "รหัสไม่พบในรายชื่อ"
		case dup[dayCode{l.Date, code}] > 1:
			lr.Check = "ซ้ำในวันเดียวกัน"
		case l.Date < van.StartDate:
			lr.Check = "วิ่งก่อนวันเริ่มวิ่ง"
		}
		if lr.Check != "" {
			r.ProblemRows++
		}
		r.Logs[i] = lr
	}
	return r
}
