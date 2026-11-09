package graph

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestMigrationDuplicateExcelPreservesRecords(t *testing.T) {
	records := []map[string]any{
		{"f_id": "9007199254740993", "f_account": "重复账号", "note": "=1+1"},
		{"f_id": "9007199254740994", "f_account": "重复账号", "note": "需要人工核对"},
	}
	response := httptest.NewRecorder()
	handleSelectedRowsExcelExport(response, "duplicates", records)
	if response.Code != 200 {
		t.Fatalf("export failed: %s", response.Body.String())
	}
	book, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows("Data")
	if err != nil || len(rows) != 3 {
		t.Fatalf("incomplete workbook: %v, %v", rows, err)
	}
	for column, name := range rows[0] {
		for index, record := range records {
			expected := record[name].(string)
			if expected == "=1+1" {
				expected = "'=1+1"
			}
			if rows[index+1][column] != expected {
				t.Fatalf("export changed %s: %q", name, rows[index+1][column])
			}
			cell, _ := excelize.CoordinatesToCellName(column+1, index+2)
			formula, err := book.GetCellFormula("Data", cell)
			if err != nil || formula != "" {
				t.Fatalf("text interpreted as a formula: %s, %v", formula, err)
			}
		}
	}
}
