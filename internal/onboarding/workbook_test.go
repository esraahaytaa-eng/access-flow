package onboarding

import (
	"archive/zip"
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func templateFile(t *testing.T) *excelize.File {
	t.Helper()
	_, s := fixture()
	s.Departments["Ürün Geliştirme"] = true
	raw, err := Template(s)
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTemplateSheetsAndDropdowns(t *testing.T) {
	f := templateFile(t)
	for _, sheet := range []string{SheetEmployees, SheetTools, SheetAccess} {
		if visible, err := f.GetSheetVisible(sheet); err != nil || !visible {
			t.Fatalf("%s visible = %v, %v", sheet, visible, err)
		}
		rows, err := f.GetRows(sheet)
		if err != nil || strings.Join(rows[0], "|") != strings.Join(headers[sheet], "|") {
			t.Fatalf("%s header = %v, %v", sheet, rows, err)
		}
	}
	if visible, _ := f.GetSheetVisible("Lists"); visible {
		t.Fatal("the Lists sheet is visible")
	}
	// Active entries only, names exactly as defined.
	depts, _ := f.GetCols("Lists")
	if got := strings.TrimRight(strings.Join(depts[0][1:], "|"), "|"); got != "Engineering|Ürün Geliştirme" { // columns are padded
		t.Fatalf("departments = %q", got)
	}
	if got := strings.Join(depts[4][1:], "|"); got != strings.Join(StepNames, "|") {
		t.Fatalf("approval steps = %q", got)
	}

	want := map[string]map[string]string{
		SheetEmployees: {"C2:C1001": "Lists!$A$2:$A$3", "D2:D1001": "Lists!$B$2:$B$2"},
		SheetTools:     {"B2:B2001": "Lists!$D$2:$D$4", "D2:D2001": "Lists!$C$2:$C$2", "F2:F2001": "Lists!$E$2:$E$5"},
	}
	for sheet, ranges := range want {
		dvs, err := f.GetDataValidations(sheet)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, dv := range dvs {
			got[dv.Sqref] = strings.Trim(dv.Formula1, "<formula1>/")
		}
		for sqref, formula := range ranges {
			if !strings.Contains(got[sqref], formula) {
				t.Errorf("%s %s dropdown = %q; want %q", sheet, sqref, got[sqref], formula)
			}
		}
	}
	if dvs, _ := f.GetDataValidations(SheetAccess); len(dvs) != 0 {
		t.Errorf("Existing Access dropdowns = %v; want none", dvs)
	}
}

// The template parses back, and its example rows are refused until replaced: it holds no
// real employee, credential or access.
func TestTemplateSampleRowsMustBeReplaced(t *testing.T) {
	_, s := fixture()
	raw, err := Template(s)
	if err != nil {
		t.Fatal(err)
	}
	wb, problems := Parse(raw)
	if len(problems) != 0 {
		t.Fatalf("parse problems = %v", problems)
	}
	if len(wb.Employees) != 1 || len(wb.Tools) != 1 || len(wb.Access) != 1 {
		t.Fatalf("workbook = %+v; want one example row per sheet", wb)
	}
	p := Validate(wb, s)
	samples := 0
	for _, pr := range p.Problems {
		if pr.Reason == "sample_row" {
			samples++
		}
	}
	if samples != 3 || len(p.Problems) != 3 {
		t.Fatalf("problems = %+v; want the three sample rows", p.Problems)
	}
	if !strings.HasSuffix(wb.Employees[0].Email, sampleDomain) || !strings.Contains(wb.Employees[0].FullName, "EXAMPLE") {
		t.Fatalf("example employee = %+v", wb.Employees[0])
	}
}

// newWorkbook writes rows (headers first) into a workbook with the three sheets.
func newWorkbook(t *testing.T, edit func(f *excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", SheetEmployees); err != nil {
		t.Fatal(err)
	}
	for _, sheet := range []string{SheetTools, SheetAccess} {
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatal(err)
		}
	}
	for sheet, h := range headers {
		if err := f.SetSheetRow(sheet, "A1", &h); err != nil {
			t.Fatal(err)
		}
	}
	if edit != nil {
		edit(f)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func row(t *testing.T, f *excelize.File, sheet, cell string, values ...any) {
	t.Helper()
	if err := f.SetSheetRow(sheet, cell, &values); err != nil {
		t.Fatal(err)
	}
}

func TestParseRows(t *testing.T) {
	raw := newWorkbook(t, func(f *excelize.File) {
		row(t, f, SheetEmployees, "A2", " ann@co.test ", "Ann", "Engineering", "", "")
		row(t, f, SheetEmployees, "A5", "bob@co.test", "Bob") // rows 3 and 4 empty
		row(t, f, SheetAccess, "A2", "ann@co.test", "Jira", "User", 12345)
	})
	wb, problems := Parse(raw)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(wb.Employees) != 2 || wb.Employees[0] != (EmployeeRow{2, "ann@co.test", "Ann", "Engineering", "", ""}) || wb.Employees[1].Row != 5 {
		t.Fatalf("employees = %+v", wb.Employees)
	}
	if len(wb.Access) != 1 || wb.Access[0].Account != "12345" || len(wb.Tools) != 0 {
		t.Fatalf("workbook = %+v", wb)
	}
}

func TestParseRefusesUnsafeOrMalformedFiles(t *testing.T) {
	valid := newWorkbook(t, nil)
	cases := []struct {
		name string
		data []byte
		want Problem
	}{
		{"not a workbook", []byte("eposta;arac\n"), Problem{Reason: "invalid_workbook"}},
		{"encrypted or legacy Excel", append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 512)...), Problem{Reason: "invalid_workbook"}},
		{"macros", withEntry(t, valid, "xl/vbaProject.bin", []byte("macro")), Problem{Reason: "macros_not_allowed"}},
		{"Excel 4.0 macro sheet", withEntry(t, valid, "xl/macrosheets/sheet1.xml", []byte("<xm/>")), Problem{Reason: "macros_not_allowed"}},
		{"formula", newWorkbook(t, func(f *excelize.File) {
			row(t, f, SheetEmployees, "A2", "ann@co.test", "", "Engineering")
			_ = f.SetCellFormula(SheetEmployees, "B2", `UPPER("ann")`)
		}), Problem{Sheet: SheetEmployees, Row: 2, Column: "B", Value: `=UPPER("ann")`, Reason: "formula_not_allowed"}},
		{"zip bomb", withEntry(t, valid, "xl/media/big.bin", make([]byte, 40<<20)), Problem{Reason: "invalid_workbook"}},
		{"missing sheet", func() []byte {
			f := excelize.NewFile()
			_ = f.SetSheetName("Sheet1", SheetEmployees)
			h := headers[SheetEmployees]
			_ = f.SetSheetRow(SheetEmployees, "A1", &h)
			b, _ := f.WriteToBuffer()
			return b.Bytes()
		}(), Problem{Sheet: SheetTools, Reason: "missing_sheet"}},
		{"wrong header", newWorkbook(t, func(f *excelize.File) { _ = f.SetCellValue(SheetTools, "E1", "Level") }),
			Problem{Sheet: SheetTools, Row: 1, Column: "E", Value: "Level", Reason: "invalid_header", Expected: "Access Level"}},
		{"a stray cell at the bottom of the sheet", newWorkbook(t, func(f *excelize.File) {
			_ = f.SetCellValue(SheetEmployees, "A1048576", "x")
		}), Problem{Sheet: SheetEmployees, Reason: "too_many_rows", Expected: strconv.Itoa(maxRows[SheetEmployees])}},
		{"too many rows", newWorkbook(t, func(f *excelize.File) {
			for i := 2; i <= maxRows[SheetEmployees]+2; i++ {
				_ = f.SetCellValue(SheetEmployees, "A"+strconv.Itoa(i), "x@co.test")
			}
		}), Problem{Sheet: SheetEmployees, Reason: "too_many_rows", Expected: strconv.Itoa(maxRows[SheetEmployees])}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse(tc.data)
			if len(problems) != 1 || problems[0] != tc.want {
				t.Fatalf("problems = %+v; want %+v", problems, tc.want)
			}
		})
	}
}

// withEntry copies a zip and adds one entry.
func withEntry(t *testing.T, data []byte, name string, content []byte) []byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		src, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		dst, err := w.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		src.Close()
	}
	dst, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
