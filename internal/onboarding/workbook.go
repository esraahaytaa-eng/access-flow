package onboarding

import (
	"archive/zip"
	"bytes"
	"slices"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// headers are each sheet's exact header row, in order.
var headers = map[string][]string{
	SheetEmployees: {"Email", "Full Name", "Department", "Job Position", "Manager Email"},
	SheetTools:     {"Tool", "Fulfillment Method", "Tool Owner Email", "Admin Group", "Access Level", "Approval Steps"},
	SheetAccess:    {"Employee Email", "Tool", "Access Level", "Account"},
}

// maxRows bounds each sheet: about 400 employees and 60 tools fit with room to spare.
var maxRows = map[string]int{SheetEmployees: 1000, SheetTools: 2000, SheetAccess: 5000}

// maxColumns bounds a sheet's width (A–Z). Loading a sheet fills every row up to its last
// cell, so one cell far to the right in each row would cost memory per row (security F-2).
const maxColumns = 26

// maxUnzipped caps the decompressed workbook, so a small upload cannot expand without bound.
const maxUnzipped = 16 << 20

const listsSheet = "Lists"

// Template builds the onboarding workbook: three sheets with their headers, one example row
// each, and dropdowns of the active departments, job positions and admin groups, the
// fulfillment methods and the approval steps (kept on a hidden Lists sheet).
func Template(s Snapshot) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", SheetEmployees); err != nil {
		return nil, err
	}
	for _, sheet := range []string{SheetTools, SheetAccess, listsSheet} {
		if _, err := f.NewSheet(sheet); err != nil {
			return nil, err
		}
	}
	if err := f.SetSheetVisible(listsSheet, false); err != nil {
		return nil, err
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, err
	}
	example, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Italic: true, Color: "808080"}})
	if err != nil {
		return nil, err
	}
	methods := []string{"Manual", "Vendor", "API"}
	lists := [][]string{active(s.Departments), active(s.Positions), sortedKeys(s.Groups), methods, StepNames}
	for i, list := range append([][]string{{"Departments", "Job Positions", "Admin Groups", "Fulfillment Methods", "Approval Steps"}}, lists...) {
		if i == 0 {
			if err := f.SetSheetRow(listsSheet, "A1", &list); err != nil {
				return nil, err
			}
			continue
		}
		col, _ := excelize.ColumnNumberToName(i)
		if err := f.SetSheetCol(listsSheet, col+"2", &list); err != nil {
			return nil, err
		}
	}
	first := func(l []string) string {
		if len(l) == 0 {
			return ""
		}
		return l[0]
	}
	examples := map[string][]any{
		SheetEmployees: {"example.employee" + sampleDomain, "EXAMPLE – replace this row", first(lists[0]), first(lists[1]), ""},
		SheetTools:     {SampleTool, "Manual", "example.owner" + sampleDomain, first(lists[2]), "User", "Manager"},
		SheetAccess:    {"example.employee" + sampleDomain, SampleTool, "User", ""},
	}
	for sheet, h := range headers {
		last, _ := excelize.ColumnNumberToName(len(h))
		if err := f.SetSheetRow(sheet, "A1", &h); err != nil {
			return nil, err
		}
		row := examples[sheet]
		if err := f.SetSheetRow(sheet, "A2", &row); err != nil {
			return nil, err
		}
		for _, e := range []error{
			f.SetCellStyle(sheet, "A1", last+"1", bold),
			f.SetCellStyle(sheet, "A2", last+"2", example),
			f.SetColWidth(sheet, "A", last, 28),
			f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}),
		} {
			if e != nil {
				return nil, e
			}
		}
	}
	for _, d := range []struct {
		sheet, column string
		list          int
	}{
		{SheetEmployees, "C", 0}, {SheetEmployees, "D", 1},
		{SheetTools, "B", 3}, {SheetTools, "D", 2}, {SheetTools, "F", 4},
	} {
		if len(lists[d.list]) == 0 {
			continue // nothing defined yet: the server reports the value instead
		}
		col, _ := excelize.ColumnNumberToName(d.list + 1)
		dv := excelize.NewDataValidation(true)
		dv.Sqref = d.column + "2:" + d.column + strconv.Itoa(maxRows[d.sheet]+1)
		dv.SetSqrefDropList(listsSheet + "!$" + col + "$2:$" + col + "$" + strconv.Itoa(len(lists[d.list])+1))
		dv.SetError(excelize.DataValidationErrorStyleStop, "Not defined in AccessFlow", "Choose a value from the list.")
		if err := f.AddDataValidation(d.sheet, dv); err != nil {
			return nil, err
		}
	}
	f.SetActiveSheet(0)
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func active(m map[string]bool) []string {
	var out []string
	for n, ok := range m {
		if ok {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// Parse reads the three sheets. Only an .xlsx without macros is read; a formula cell is
// refused, never evaluated. Problems stop the import.
func Parse(data []byte) (Workbook, []Problem) {
	bad := func(reason string) (Workbook, []Problem) { return Workbook{}, []Problem{{Reason: reason}} }
	// An .xlsx is a zip; an encrypted workbook or an old .xls is not.
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return bad("invalid_workbook")
	}
	for _, e := range z.File {
		name := strings.ToLower(e.Name)
		if name == "xl/vbaproject.bin" || strings.HasPrefix(name, "xl/macrosheets/") {
			return bad("macros_not_allowed")
		}
	}
	// excelize counts what it really decompresses, not what the zip headers claim.
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: maxUnzipped, UnzipXMLSizeLimit: maxUnzipped})
	if err != nil {
		return bad("invalid_workbook")
	}
	defer f.Close()

	var wb Workbook
	for _, sheet := range []string{SheetEmployees, SheetTools, SheetAccess} {
		rows, problem := readSheet(f, sheet)
		if problem != nil {
			return Workbook{}, []Problem{*problem}
		}
		for _, r := range rows {
			c := r.cells
			switch sheet {
			case SheetEmployees:
				wb.Employees = append(wb.Employees, EmployeeRow{r.row, c[0], c[1], c[2], c[3], c[4]})
			case SheetTools:
				wb.Tools = append(wb.Tools, ToolRow{r.row, c[0], c[1], c[2], c[3], c[4], c[5]})
			default:
				wb.Access = append(wb.Access, AccessRow{r.row, c[0], c[1], c[2], c[3]})
			}
		}
	}
	return wb, nil
}

type sheetRow struct {
	row   int
	cells []string // trimmed, one per header
}

// readSheet checks the header and returns the non-empty rows.
func readSheet(f *excelize.File, sheet string) ([]sheetRow, *Problem) {
	if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
		return nil, &Problem{Sheet: sheet, Reason: "missing_sheet"}
	}
	if p := checkSize(f, sheet); p != nil {
		return nil, p
	}
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, &Problem{Sheet: sheet, Reason: "invalid_workbook"}
	}
	h := headers[sheet]
	for i, want := range h {
		got := ""
		if len(rows) > 0 && i < len(rows[0]) {
			got = strings.TrimSpace(rows[0][i])
		}
		if got != want {
			col, _ := excelize.ColumnNumberToName(i + 1)
			return nil, &Problem{Sheet: sheet, Row: 1, Column: col, Value: got, Reason: "invalid_header", Expected: want}
		}
	}
	var out []sheetRow
	for i := 1; i < len(rows); i++ {
		cells := make([]string, len(h))
		empty := true
		for j := range cells {
			if j < len(rows[i]) {
				cells[j] = strings.TrimSpace(rows[i][j])
			}
			if cells[j] == "" {
				continue // a formula without a stored value imports nothing
			}
			empty = false
			col, _ := excelize.ColumnNumberToName(j + 1)
			// A formula's stored value may be stale; the import reads typed values only.
			if formula, _ := f.GetCellFormula(sheet, col+strconv.Itoa(i+1)); formula != "" {
				return nil, &Problem{Sheet: sheet, Row: i + 1, Column: col, Value: "=" + formula, Reason: "formula_not_allowed"}
			}
		}
		if empty {
			continue
		}
		if len(out) == maxRows[sheet] {
			return nil, &Problem{Sheet: sheet, Reason: "too_many_rows", Expected: strconv.Itoa(maxRows[sheet])}
		}
		out = append(out, sheetRow{i + 1, cells})
	}
	return out, nil
}

// checkSize streams the sheet before it is loaded and refuses one too long or too wide: a
// stray cell far below or to the right of the data would make loading it expensive.
func checkSize(f *excelize.File, sheet string) *Problem {
	rows, err := f.Rows(sheet)
	if err != nil {
		return &Problem{Sheet: sheet, Reason: "invalid_workbook"}
	}
	defer rows.Close()
	for n := 1; rows.Next(); n++ {
		if n-1 > 2*maxRows[sheet] {
			return &Problem{Sheet: sheet, Reason: "too_many_rows", Expected: strconv.Itoa(maxRows[sheet])}
		}
		cols, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return &Problem{Sheet: sheet, Reason: "invalid_workbook"}
		}
		if len(cols) > maxColumns {
			return &Problem{Sheet: sheet, Row: n, Reason: "too_many_columns", Expected: "Z"}
		}
	}
	if rows.Error() != nil {
		return &Problem{Sheet: sheet, Reason: "invalid_workbook"}
	}
	return nil
}
