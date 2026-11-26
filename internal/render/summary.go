package render

import (
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

// Summary renders vigil.summary/v0 as a header block and its tables. The data
// blocks are not printed. A stale source is shown with its age, on its verdict
// and on the hosts that came from it, so nothing old reads as current.
func Summary(w io.Writer, d *summary.Document) error {
	for _, section := range []func(io.Writer, *summary.Document) error{
		summaryHead, summaryVerdicts, summaryHosts, summarySources, summaryDrift,
	} {
		if err := section(w, d); err != nil {
			return err
		}
	}
	return nil
}

func summaryHead(w io.Writer, d *summary.Document) error {
	overall := "unavailable"
	if d.Overall != nil {
		overall = *d.Overall
	}
	rows := [][2]string{{"OVERALL", overall}, {"GENERATED_AT", d.GeneratedAt}}
	if d.Vantage != nil {
		rows = append(rows, [2]string{"VANTAGE", d.Vantage.ID}, [2]string{"VANTAGE_STATUS", d.Vantage.Status})
	}
	return pairs(w, append(rows, [2]string{"DECLARATION", cell(d.Declaration)}))
}

func summaryVerdicts(w io.Writer, d *summary.Document) error {
	type v = summary.Verdict
	return columns(w, d.Verdicts, []column[v]{
		{head: "SOURCE", value: func(r v) string { return r.Source }},
		{head: "STATE", value: func(r v) string { return r.State }},
		{head: "SEVERITY", value: func(r v) string { return cell(r.Severity) }},
		{head: "OBSERVED_AT", value: func(r v) string { return cell(r.ObservedAt) }},
		{head: "AGE", optional: true, value: func(r v) string { return age(d.GeneratedAt, r.ObservedAt) }},
		{head: "RECEIVED_AT", optional: true, value: func(r v) string { return cell(r.ReceivedAt) }},
		{head: "VALID_FOR", optional: true, value: func(r v) string { return number(r.ValidFor) }},
		{head: "DECLARATION", optional: true, value: func(r v) string { return cell(r.Declaration) }},
		{head: "REASONS", value: func(r v) string { return list(r.Reasons) }},
	})
}

// summaryHosts is the Simplex matrix. When Simplex is stale each row says so,
// with how old it is.
func summaryHosts(w io.Writer, d *summary.Document) error {
	if len(d.Hosts) == 0 {
		return nil
	}
	type h = simplexdoc.Host
	state, old := "-", "-"
	for _, v := range d.Verdicts {
		if v.Source == simplexdoc.Source && v.State == "stale" {
			state, old = v.State, age(d.GeneratedAt, v.ObservedAt)
		}
	}
	return columns(w, d.Hosts, []column[h]{
		{head: "HOST", value: func(r h) string { return r.ID }},
		{head: "SEVERITY", value: func(r h) string { return r.Severity }},
		{head: "STATE", optional: true, value: func(h) string { return state }},
		{head: "AGE", optional: true, value: func(h) string { return old }},
		{head: "REASONS", value: func(r h) string { return list(r.Reasons) }},
	})
}

func summarySources(w io.Writer, d *summary.Document) error {
	type s = summary.Source
	return columns(w, d.Sources, []column[s]{
		{head: "ID", value: func(r s) string { return r.ID }},
		{head: "KIND", value: func(r s) string { return r.Kind }},
		{head: "STATE", value: func(r s) string { return r.State }},
		{head: "OBSERVED_AT", value: func(r s) string { return cell(r.ObservedAt) }},
		{head: "LAST_OK", value: func(r s) string { return cell(r.LastOK) }},
		{head: "DETAIL_CODE", optional: true, value: func(r s) string { return cell(r.DetailCode) }},
		{head: "VANTAGE", optional: true, value: func(r s) string { return cellStr(r.Vantage) }},
	})
}

func summaryDrift(w io.Writer, d *summary.Document) error {
	drift := d.DeclarationDrift
	if drift == nil {
		return nil
	}
	if err := writef(w, "\n"); err != nil {
		return err
	}
	if err := pairs(w, [][2]string{
		{"DRIFTED", strconv.FormatBool(drift.Drifted)},
		{"IN_GRACE", strconv.FormatBool(drift.InGrace)},
		{"REFERENCE", cell(drift.Reference)},
		{"UNDECLARED", list(drift.Undeclared)},
	}); err != nil {
		return err
	}
	if len(drift.ByDeclaration) == 0 {
		return nil
	}
	type g = summary.Group
	return columns(w, drift.ByDeclaration, []column[g]{
		{head: "DECLARATION", value: func(r g) string { return r.Declaration }},
		{head: "SUBJECTS", value: func(r g) string { return list(r.Subjects) }},
	})
}

// pairs writes an aligned key and value block.
func pairs(w io.Writer, rows [][2]string) error {
	tw := newTabWriter(w)
	for _, kv := range rows {
		if err := writef(tw, "%s\t%s\n", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// column is one table column. An optional one appears only when some row has
// something to put in it.
type column[T any] struct {
	head     string
	value    func(T) string
	optional bool
}

// columns writes a blank line and then the table.
func columns[T any](w io.Writer, rows []T, all []column[T]) error {
	if err := writef(w, "\n"); err != nil {
		return err
	}
	shown := slices.DeleteFunc(all, func(c column[T]) bool {
		return c.optional && !slices.ContainsFunc(rows, func(r T) bool { return c.value(r) != "-" })
	})
	tw := newTabWriter(w)
	line := make([]string, len(shown))
	for i, c := range shown {
		line[i] = c.head
	}
	if err := writef(tw, "%s\n", strings.Join(line, "\t")); err != nil {
		return err
	}
	for _, r := range rows {
		for i, c := range shown {
			line[i] = c.value(r)
		}
		if err := writef(tw, "%s\n", strings.Join(line, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// age is how long before the document's own time then was, in whole seconds, or
// a dash when either time is missing or unreadable.
func age(generatedAt string, then *string) string {
	if then == nil {
		return "-"
	}
	now, nowErr := time.Parse(time.RFC3339Nano, generatedAt)
	at, atErr := time.Parse(time.RFC3339Nano, *then)
	if nowErr != nil || atErr != nil {
		return "-"
	}
	return strconv.FormatInt(int64(now.Sub(at)/time.Second), 10) + "s"
}

func number(n *int) string {
	if n == nil {
		return "-"
	}
	return strconv.Itoa(*n)
}

// list joins codes or ids with commas, or a dash when there are none.
func list(items []string) string { return cellStr(strings.Join(items, ",")) }
