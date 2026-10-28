package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	sheetSettings = "ตั้งค่า"
	sheetVans     = "รายชื่อรถตู้"
	sheetLogs     = "บันทึกการวิ่งประจำวัน"
)

// readWorkbook loads the input cells of the original Google Sheet
// (File > Download > Microsoft Excel). Formula columns are recomputed by Compute.
func readWorkbook(path string) (Settings, []Van, []RunLog, error) {
	var st Settings
	f, err := excelize.OpenFile(path)
	if err != nil {
		return st, nil, nil, err
	}
	defer f.Close()
	raw := excelize.Options{RawCellValue: true}

	dateCell, _ := f.GetCellValue(sheetSettings, "C4", raw)
	if st.Date, err = excelSerialDay(dateCell); err != nil || st.Date == 0 {
		st.Date = Today()
	}
	neededCell, _ := f.GetCellValue(sheetSettings, "C5", raw)
	st.Needed = atoi(neededCell)

	rows, err := f.GetRows(sheetVans, raw)
	if err != nil {
		return st, nil, nil, fmt.Errorf("%s: %w", sheetVans, err)
	}
	var vans []Van
	for i := 3; i < len(rows); i++ { // data starts on row 4
		c := cells(rows[i])
		if c(2) == "" { // the sheet ignores rows without a driver name
			continue
		}
		v := Van{
			Code: NormCode(c(1)), Driver: c(2), Plate: c(3), Phone: c(4), Seats: atoi(c(5)),
			Note: c(6), Zone: c(7), Status: c(8),
		}
		var errs [3]error
		v.StartDate, errs[0] = excelSerialDay(c(9))
		v.LeaveStart, errs[1] = excelSerialDay(c(10))
		v.LeaveEnd, errs[2] = excelSerialDay(c(11))
		for _, e := range errs {
			if e != nil {
				return st, nil, nil, fmt.Errorf("%s row %d: %w", sheetVans, i+1, e)
			}
		}
		vans = append(vans, v)
	}

	rows, err = f.GetRows(sheetLogs, raw)
	if err != nil {
		return st, nil, nil, fmt.Errorf("%s: %w", sheetLogs, err)
	}
	var logs []RunLog
	for i := 1; i < len(rows); i++ {
		c := cells(rows[i])
		if c(0) == "" && c(1) == "" {
			continue
		}
		d, err := excelSerialDay(c(0))
		if err != nil {
			return st, nil, nil, fmt.Errorf("%s row %d: %w", sheetLogs, i+1, err)
		}
		logs = append(logs, RunLog{Date: d, Code: NormCode(c(1)), Destination: c(3), Status: c(4), Note: c(5)})
	}
	return st, vans, logs, nil
}

func cells(row []string) func(int) string {
	return func(i int) string {
		if i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
}

func atoi(s string) int {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return int(f)
}
