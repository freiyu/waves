package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"regexp"

	"waves/internal/api/contract"
)

// The arrangement of the Graph page is the human's own tidy-up: node places and the side of each line. It carries no
// authority, but it is still input from the browser, so it is checked whole and refused whole.
const (
	graphMaxBytes = 256 << 10
	graphMaxNodes = 2000
	graphMaxLines = 4000
	graphMaxCoord = 100000
)

var graphKey = regexp.MustCompile(`^[A-Za-z0-9_.:>#-]{1,120}$`)

var graphSides = map[string]bool{"top": true, "right": true, "bottom": true, "left": true}

func validGraphLayout(l contract.GraphLayout) *Error {
	bad := func(msg string) *Error {
		return errValidation("The arrangement is not valid: "+msg+".", "Reload the Graph and try again.")
	}
	if len(l.Nodes) > graphMaxNodes || len(l.Lines) > graphMaxLines {
		return bad("too many nodes or lines")
	}
	for k, v := range l.Nodes {
		if !graphKey.MatchString(k) {
			return bad("a node key has an unexpected shape")
		}
		if math.IsNaN(v.X) || math.IsNaN(v.Y) || math.Abs(v.X) > graphMaxCoord || math.Abs(v.Y) > graphMaxCoord {
			return bad("a node is outside the canvas")
		}
	}
	unit := func(f *float64) bool { return f == nil || (*f >= 0 && *f <= 1) }
	for k, v := range l.Lines {
		if !graphKey.MatchString(k) {
			return bad("a line key has an unexpected shape")
		}
		if !graphSides[v.From] || !graphSides[v.To] || !unit(v.Fo) || !unit(v.Tt) {
			return bad("a line has an unknown side or position")
		}
	}
	return nil
}

// GraphLayout returns the saved arrangement (empty when none was saved).
func (s *Service) GraphLayout(ctx context.Context) (contract.GraphLayout, error) {
	out := contract.GraphLayout{Nodes: map[string]contract.GraphNodePos{}, Lines: map[string]contract.GraphLine{}}
	var raw string
	err := s.db.R.QueryRowContext(ctx, `SELECT layout_json FROM graph_layout WHERE id = 1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, wrap(err, "read graph layout")
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return contract.GraphLayout{Nodes: map[string]contract.GraphNodePos{}, Lines: map[string]contract.GraphLine{}}, nil // damaged: start clean, never block the page
	}
	if out.Nodes == nil {
		out.Nodes = map[string]contract.GraphNodePos{}
	}
	if out.Lines == nil {
		out.Lines = map[string]contract.GraphLine{}
	}
	return out, nil
}

// SetGraphLayout replaces the saved arrangement. Only the operator session or the CLI operator token may; agents,
// leases and the guard never can.
func (s *Service) SetGraphLayout(ctx context.Context, p Principal, l contract.GraphLayout) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	if l.Nodes == nil {
		l.Nodes = map[string]contract.GraphNodePos{}
	}
	if l.Lines == nil {
		l.Lines = map[string]contract.GraphLine{}
	}
	if e := validGraphLayout(l); e != nil {
		return e
	}
	b, err := json.Marshal(l)
	if err != nil {
		return wrap(err, "encode graph layout")
	}
	if len(b) > graphMaxBytes {
		return errValidation("The arrangement is too large.", "Reload the Graph and try again.")
	}
	return s.tx(ctx, func(t *Tx) error {
		_, err := t.ExecContext(ctx, `INSERT INTO graph_layout(id, layout_json, updated_at) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET layout_json = excluded.layout_json, updated_at = excluded.updated_at`, string(b), s.ts())
		return wrap(err, "save graph layout")
	})
}
