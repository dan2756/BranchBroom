package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/width"
)

// status is the human-readable merge state of a branch.
func status(b Branch) string {
	switch {
	case b.Merged:
		return "merged"
	case b.Squashed:
		return "squashed"
	}
	return "unmerged"
}

// relativeAge renders how long ago t was, git-style.
func relativeAge(t, now time.Time) string {
	d := now.Sub(t)
	const day = 24 * time.Hour
	switch {
	case d < 0:
		return "in the future"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < day:
		return plural(int(d/time.Hour), "hour") + " ago"
	case d < 14*day:
		return plural(int(d/day), "day") + " ago"
	case d < 60*day:
		return plural(int(d/(7*day)), "week") + " ago"
	case d < 365*day:
		return plural(int(d/(30*day)), "month") + " ago"
	default:
		return plural(int(d/(365*day)), "year") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// outputFormat selects how branches are printed.
type outputFormat int

const (
	formatTable outputFormat = iota
	formatJSON
	formatPorcelain
)

// ANSI styles used in the table.
const (
	styleBold   = "1"
	styleDim    = "2"
	styleGreen  = "32"
	styleYellow = "33"
	styleCyan   = "36"
)

func paint(s, style string, color bool) string {
	if !color || s == "" || style == "" {
		return s
	}
	return "\x1b[" + style + "m" + s + "\x1b[0m"
}

func statusStyle(st string) string {
	switch st {
	case "unmerged":
		return styleYellow
	case "squashed":
		return styleCyan
	}
	return styleGreen
}

// listMeta describes what the branches were compared against.
type listMeta struct {
	base   string // e.g. main or origin/main
	remote string // set when listing a remote's branches
}

// writeBranches prints branches in the requested format.
func writeBranches(w io.Writer, format outputFormat, branches []Branch, meta listMeta, now time.Time, color bool) error {
	switch format {
	case formatJSON:
		return writeJSON(w, branches, meta, now)
	case formatPorcelain:
		for _, b := range branches {
			if _, err := fmt.Fprintln(w, b.Name); err != nil {
				return err
			}
		}
		return nil
	default:
		return writeTable(w, branches, now, color)
	}
}

// writeTable prints branches as an aligned table, colored when color is
// set. Alignment is computed on the plain text.
func writeTable(w io.Writer, branches []Branch, now time.Time, color bool) error {
	header := []string{"BRANCH", "AGE", "AUTHOR", "STATUS", "AHEAD", "BEHIND"}
	rightAlign := []bool{false, false, false, false, true, true}
	withNote := false
	for _, b := range branches {
		withNote = withNote || b.Protected != ""
	}
	if withNote {
		header = append(header, "NOTE")
		rightAlign = append(rightAlign, false)
	}
	styles := [][]string{make([]string, len(header))}
	for i := range header {
		styles[0][i] = styleBold
	}
	rows := [][]string{header}
	for _, b := range branches {
		st := status(b)
		row := []string{
			b.Name,
			relativeAge(b.Date, now),
			b.Author,
			st,
			strconv.Itoa(b.Ahead),
			strconv.Itoa(b.Behind),
		}
		style := []string{"", "", "", statusStyle(st), "", ""}
		if withNote {
			row = append(row, b.Protected)
			style = append(style, styleDim)
		}
		rows = append(rows, row)
		styles = append(styles, style)
	}
	widths := make([]int, len(header))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], displayWidth(cell))
		}
	}
	for r, row := range rows {
		var sb strings.Builder
		for i, cell := range row {
			pad := strings.Repeat(" ", widths[i]-displayWidth(cell))
			if i > 0 {
				sb.WriteString("  ")
			}
			cell = paint(cell, styles[r][i], color)
			if rightAlign[i] {
				sb.WriteString(pad + cell)
			} else {
				sb.WriteString(cell + pad)
			}
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(sb.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}

// displayWidth is the number of terminal columns s takes up: wide and
// fullwidth characters, e.g. CJK, take two. Combining marks and format
// characters (variation selectors, zero-width spaces, ZWJ) take none;
// East Asian width calls some of them ambiguous, which would pad the row.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}

type jsonBranch struct {
	Name      string `json:"name"`
	Ref       string `json:"ref"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	AgeDays   int    `json:"age_days"`
	Author    string `json:"author"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	Merged    bool   `json:"merged"`
	Squashed  bool   `json:"squashed"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Current   bool   `json:"current"`
	Protected bool   `json:"protected"`
	Note      string `json:"note,omitempty"`
}

type jsonOutput struct {
	Base     string       `json:"base"`
	Remote   string       `json:"remote,omitempty"`
	Branches []jsonBranch `json:"branches"`
}

func writeJSON(w io.Writer, branches []Branch, meta listMeta, now time.Time) error {
	out := jsonOutput{Base: meta.base, Remote: meta.remote, Branches: []jsonBranch{}}
	for _, b := range branches {
		out.Branches = append(out.Branches, jsonBranch{
			Name:      b.Name,
			Ref:       b.Ref,
			Commit:    b.Commit,
			Date:      b.Date.UTC().Format(time.RFC3339),
			AgeDays:   int(now.Sub(b.Date) / (24 * time.Hour)),
			Author:    b.Author,
			Email:     b.Email,
			Status:    status(b),
			Merged:    b.Merged,
			Squashed:  b.Squashed,
			Ahead:     b.Ahead,
			Behind:    b.Behind,
			Current:   b.Current,
			Protected: b.Protected != "",
			Note:      b.Protected,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
