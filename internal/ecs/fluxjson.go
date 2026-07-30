package ecs

import "encoding/json"

// The Flux API answers with a columnar envelope:
//
//	{"Series":[{"Datatypes":[…],
//	            "Columns":["table","_start","_stop","_time","_value","_field","_measurement","host","node_id","process","tag"],
//	            "Values":[["0","2020-…Z","2020-…Z","2020-…Z","1","failed_request_counter","statDataHead_…","ecs.lss.emc.com","28cd…","statDataHead","dashboard"]]}]}
//
// Every cell is a JSON string, numbers included, and column order is not part of
// the contract. This file resolves columns by name and yields nothing for a cell
// it cannot read, so an unreadable value produces an absent sample rather than a
// zero (ADR-0007) — the same discipline points.go applies to the dashboard's
// time series, for the same reason.

// fluxResp is the decoded query response.
type fluxResp struct {
	Series []fluxSeries `json:"Series"`
}

// fluxSeries is one result table. Cells stay raw so the shared tolerant scalar
// parsing decides what counts as absent.
type fluxSeries struct {
	Columns []string            `json:"Columns"`
	Values  [][]json.RawMessage `json:"Values"`
}

// fluxRow is one result row, addressable by column name. Columns whose cell held
// nothing are absent from the map rather than present and empty.
type fluxRow struct {
	cols map[string]string
}

// rows flattens every Series into name-addressable rows. A row whose width
// disagrees with its column list is dropped: it cannot be aligned, so mapping it
// would attach values to the wrong keys.
func (r fluxResp) rows() []fluxRow {
	var out []fluxRow
	for _, s := range r.Series {
		for _, raw := range s.Values {
			if len(raw) != len(s.Columns) {
				continue
			}
			cols := make(map[string]string, len(raw))
			for i, cell := range raw {
				if v, ok := cleanScalar(cell); ok {
					cols[s.Columns[i]] = v
				}
			}
			out = append(out, fluxRow{cols: cols})
		}
	}
	return out
}

// value returns the row's cell for a column, and whether it carried anything.
func (r fluxRow) value(col string) (string, bool) {
	v, ok := r.cols[col]
	return v, ok
}

// num parses a column as a float. A missing column and an unparseable one are
// both absent — neither is zero.
func (r fluxRow) num(col string) (float64, bool) {
	s, ok := r.cols[col]
	if !ok {
		return 0, false
	}
	return anyToFloat(s)
}
