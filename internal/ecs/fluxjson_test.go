package ecs

import (
	"encoding/json"
	"testing"
)

func decodeFlux(t *testing.T, body string) fluxResp {
	t.Helper()
	var r fluxResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFluxRowsAddressColumnsByName(t *testing.T) {
	rows := decodeFlux(t, fixture(t, "flux_transactions.json")).rows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	field, ok := rows[0].value("_field")
	if !ok || field != "failed_request_counter" {
		t.Errorf("_field = %q (ok=%v), want failed_request_counter", field, ok)
	}
	v, ok := rows[0].num("_value")
	if !ok || v != 17 {
		t.Errorf("_value = %v (ok=%v), want 17", v, ok)
	}
	if host, _ := rows[1].value("host"); host != "supr01-r01.example.com" {
		t.Errorf("host = %q, want supr01-r01.example.com", host)
	}
}

func TestFluxRowsToleratesReorderedColumns(t *testing.T) {
	// Column order is not part of the contract; only names are.
	rows := decodeFlux(t, `{"Series":[{"Columns":["_value","_field","host"],
		"Values":[["3","usage_user","n1"]]}]}`).rows()
	v, ok := rows[0].num("_value")
	if !ok || v != 3 {
		t.Errorf("_value = %v (ok=%v), want 3", v, ok)
	}
}

func TestFluxRowsSkipsMisalignedRows(t *testing.T) {
	// A row whose width disagrees with its column list cannot be aligned, so it
	// cannot be trusted — silently mapping it would attach values to the wrong keys.
	rows := decodeFlux(t, `{"Series":[{"Columns":["_value","_field"],
		"Values":[["3"],["4","usage_user"]]}]}`).rows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want only the aligned one", len(rows))
	}
}

func TestFluxRowsTreatNullAndNAAsAbsent(t *testing.T) {
	rows := decodeFlux(t, `{"Series":[{"Columns":["_value","_field","host"],
		"Values":[[null,"usage_user","n1"],["N/A","usage_user","n2"]]}]}`).rows()
	for i, r := range rows {
		if _, ok := r.num("_value"); ok {
			t.Errorf("row %d yielded a value for an absent cell", i)
		}
	}
}

func TestFluxRowsHandlesEmptyAndMissingSeries(t *testing.T) {
	for _, body := range []string{
		`{"Series":[]}`,
		`{}`,
		`{"Series":[{"Columns":["_value"],"Values":[]}]}`,
	} {
		if got := len(decodeFlux(t, body).rows()); got != 0 {
			t.Errorf("%s yielded %d rows, want 0", body, got)
		}
	}
}

func TestFluxRowsMissingColumnIsAbsentNotZero(t *testing.T) {
	rows := decodeFlux(t, `{"Series":[{"Columns":["_field"],"Values":[["usage_user"]]}]}`).rows()
	if _, ok := rows[0].num("_value"); ok {
		t.Error("num returned ok for a column that is not present")
	}
}
