// Package render turns the four V1 backend documents into the two shapes a
// caller sees: the human table and the byte-stable JSON line. The document
// types live with their producers (promclient, grafana, sources); this
// package imports and renders them, and defines none of its own. A table is
// a pure renderer of one document: it never prints a field the document
// lacks, and it never re-derives data.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Hedronite/vigil/internal/grafana"
	"github.com/Hedronite/vigil/internal/promclient"
	"github.com/Hedronite/vigil/internal/sources"
)

// JSON writes the document alone, one line, byte-stable: the same document
// always produces the same bytes.
func JSON(w io.Writer, doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("render json: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", b); err != nil {
		return fmt.Errorf("render json: %w", err)
	}
	return nil
}

// writef writes one formatted chunk and returns any write error.
func writef(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

// Targets renders vigil.targets/v0 as a table.
func Targets(w io.Writer, d *promclient.TargetsDoc) error {
	tw := newTabWriter(w)
	for _, kv := range [][2]string{
		{"BACKEND", d.Backend},
		{"VANTAGE", d.Vantage},
		{"GENERATED_AT", d.GeneratedAt},
	} {
		if err := writef(tw, "%s\t%s\n", kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if err := writef(w, "\n"); err != nil {
		return err
	}
	tw = newTabWriter(w)
	if err := writef(tw, "TARGET\tJOB\tHEALTH\tLAST_SCRAPE\n"); err != nil {
		return err
	}
	for _, r := range d.Targets {
		if err := writef(tw, "%s\t%s\t%s\t%s\n", r.Host, r.Job, r.Health, cell(r.LastScrape)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// Query renders vigil.query/v0 as a table. The HOST line appears only when
// the query was run for one host; the JOB column appears only when some
// series has a publishable job.
func Query(w io.Writer, d *promclient.QueryDoc) error {
	tw := newTabWriter(w)
	head := [][2]string{
		{"BACKEND", d.Backend},
		{"VANTAGE", d.Vantage},
		{"NAME", d.Name},
	}
	if d.Host != "" {
		head = append(head, [2]string{"HOST", d.Host})
	}
	head = append(head, [2]string{"EVALUATED_AT", d.EvaluatedAt})
	for _, kv := range head {
		if err := writef(tw, "%s\t%s\n", kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if err := writef(w, "\n"); err != nil {
		return err
	}
	tw = newTabWriter(w)
	if anyJob(d.Series) {
		if err := writef(tw, "HOST\tJOB\tVALUE\n"); err != nil {
			return err
		}
		for _, r := range d.Series {
			if err := writef(tw, "%s\t%s\t%s\n", r.Host, cellStr(r.Job), r.Value); err != nil {
				return err
			}
		}
	} else {
		if err := writef(tw, "HOST\tVALUE\n"); err != nil {
			return err
		}
		for _, r := range d.Series {
			if err := writef(tw, "%s\t%s\n", r.Host, r.Value); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}

// Health renders vigil.health/v0 as a key-value block. The DETAIL_CODE line
// appears only when the document carries the field.
func Health(w io.Writer, d *grafana.HealthDoc) error {
	tw := newTabWriter(w)
	rows := [][2]string{
		{"BACKEND", d.Backend},
		{"STATE", d.State},
	}
	if d.DetailCode != nil {
		rows = append(rows, [2]string{"DETAIL_CODE", cell(d.DetailCode)})
	}
	rows = append(rows, [2]string{"CHECKED_AT", d.CheckedAt})
	for _, kv := range rows {
		if err := writef(tw, "%s\t%s\n", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// Sources renders vigil.sources/v0 as a table. Observed at and last ok are
// always carried, so their columns always exist; the DETAIL_CODE and VANTAGE
// columns appear only when some row carries the field.
func Sources(w io.Writer, d *sources.Doc) error {
	tw := newTabWriter(w)
	if err := writef(tw, "GENERATED_AT\t%s\n", d.GeneratedAt); err != nil {
		return err
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	detail := anySource(d.Sources, func(s sources.Source) bool { return s.DetailCode != nil })
	vantage := anySource(d.Sources, func(s sources.Source) bool { return s.Vantage != "" })
	tw = newTabWriter(w)
	if err := writef(w, "\n"); err != nil {
		return err
	}
	if err := writef(tw, "ID\tKIND\tSTATE\tOBSERVED_AT\tLAST_OK"); err != nil {
		return err
	}
	if detail {
		if err := writef(tw, "\tDETAIL_CODE"); err != nil {
			return err
		}
	}
	if vantage {
		if err := writef(tw, "\tVANTAGE"); err != nil {
			return err
		}
	}
	if err := writef(tw, "\n"); err != nil {
		return err
	}
	for _, r := range d.Sources {
		if err := writef(tw, "%s\t%s\t%s\t%s\t%s", r.ID, r.Kind, r.State, cell(r.ObservedAt), cell(r.LastOK)); err != nil {
			return err
		}
		if detail {
			if err := writef(tw, "\t%s", cell(r.DetailCode)); err != nil {
				return err
			}
		}
		if vantage {
			if err := writef(tw, "\t%s", cellStr(r.Vantage)); err != nil {
				return err
			}
		}
		if err := writef(tw, "\n"); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// newTabWriter aligns columns with two spaces of padding. Rows end without a
// trailing tab, so no line carries trailing spaces.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// cell renders a nullable string: a dash when it is null.
func cell(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// cellStr renders an optional plain string: a dash when the key is absent.
func cellStr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// anyJob reports whether some series carries a publishable job.
func anyJob(series []promclient.Series) bool {
	for _, r := range series {
		if r.Job != "" {
			return true
		}
	}
	return false
}

// anySource reports whether some row satisfies f.
func anySource(rows []sources.Source, f func(sources.Source) bool) bool {
	for _, r := range rows {
		if f(r) {
			return true
		}
	}
	return false
}
