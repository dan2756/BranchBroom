package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRelativeAge(t *testing.T) {
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{-time.Hour, "in the future"},
		{10 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{45 * time.Minute, "45 minutes ago"},
		{5 * time.Hour, "5 hours ago"},
		{day, "1 day ago"},
		{13 * day, "13 days ago"},
		{15 * day, "2 weeks ago"},
		{59 * day, "8 weeks ago"},
		{90 * day, "3 months ago"},
		{364 * day, "12 months ago"},
		{400 * day, "1 year ago"},
		{800 * day, "2 years ago"},
	}
	for _, tt := range tests {
		if got := relativeAge(refTime.Add(-tt.ago), refTime); got != tt.want {
			t.Errorf("relativeAge(%v) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}

func TestWriteTable(t *testing.T) {
	branches := []Branch{
		{Name: "feature/login", Date: refTime.Add(-120 * day), Author: "Alice", Merged: true, Behind: 12},
		{Name: "wip", Date: refTime.Add(-3 * time.Hour), Author: "Bob Smith", Ahead: 104, Behind: 3},
	}
	var buf bytes.Buffer
	if err := writeTable(&buf, branches, refTime, false); err != nil {
		t.Fatal(err)
	}
	want := "" +
		"BRANCH         AGE           AUTHOR     STATUS    AHEAD  BEHIND\n" +
		"feature/login  4 months ago  Alice      merged        0      12\n" +
		"wip            3 hours ago   Bob Smith  unmerged    104       3\n"
	if got := buf.String(); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteTableWideCharacters(t *testing.T) {
	branches := []Branch{
		{Name: "feature/日本語", Date: refTime.Add(-120 * day), Author: "山田", Merged: true, Behind: 12},
		{Name: "feature/login", Date: refTime.Add(-3 * time.Hour), Author: "Bob", Ahead: 1, Behind: 3},
	}
	var buf bytes.Buffer
	if err := writeTable(&buf, branches, refTime, false); err != nil {
		t.Fatal(err)
	}
	want := "" +
		"BRANCH          AGE           AUTHOR  STATUS    AHEAD  BEHIND\n" +
		"feature/日本語  4 months ago  山田    merged        0      12\n" +
		"feature/login   3 hours ago   Bob     unmerged      1       3\n"
	if got := buf.String(); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestWriteTableCombiningMarks(t *testing.T) {
	if got := displayWidth("a\u0301"); got != 1 {
		t.Fatalf("combining acute width %d, want 1", got)
	}
	if got := displayWidth("\u200b"); got != 0 {
		t.Fatalf("zero-width space width %d, want 0", got)
	}
	branches := []Branch{
		{Name: "cafe\u0301", Date: refTime.Add(-time.Hour), Author: "A\u0301lice", Ahead: 1},
		{Name: "cafe", Date: refTime.Add(-2 * time.Hour), Author: "Alice", Ahead: 2},
	}
	var buf bytes.Buffer
	if err := writeTable(&buf, branches, refTime, false); err != nil {
		t.Fatal(err)
	}
	want := "" +
		"BRANCH  AGE          AUTHOR  STATUS    AHEAD  BEHIND\n" +
		"cafe\u0301    1 hour ago   A\u0301lice   unmerged      1       0\n" +
		"cafe    2 hours ago  Alice   unmerged      2       0\n"
	if got := buf.String(); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteTableColor(t *testing.T) {
	branches := []Branch{
		{Name: "feature/login", Date: refTime.Add(-120 * day), Author: "Alice", Merged: true, Behind: 12},
		{Name: "main", Date: refTime.Add(-time.Hour), Author: "Bob", Merged: true, Current: true, Protected: "current"},
		{Name: "wip", Date: refTime.Add(-3 * time.Hour), Author: "Bob Smith", Ahead: 104, Behind: 3},
	}
	var plain, colored bytes.Buffer
	if err := writeTable(&plain, branches, refTime, false); err != nil {
		t.Fatal(err)
	}
	if err := writeTable(&colored, branches, refTime, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Error("plain table contains escape codes")
	}
	for _, want := range []string{"\x1b[1mBRANCH\x1b[0m", "\x1b[32mmerged\x1b[0m", "\x1b[33munmerged\x1b[0m", "\x1b[2mcurrent\x1b[0m"} {
		if !strings.Contains(colored.String(), want) {
			t.Errorf("colored table lacks %q:\n%s", want, colored.String())
		}
	}
	if strings.Contains(colored.String(), "\x1b[m") {
		t.Errorf("unstyled cells are wrapped in escape codes:\n%q", colored.String())
	}
	if got := ansiRE.ReplaceAllString(colored.String(), ""); got != plain.String() {
		t.Errorf("colors change alignment:\n%s\nvs\n%s", got, plain.String())
	}
}

func TestColorDetection(t *testing.T) {
	r := fixture(t)
	tests := []struct {
		tty  bool
		vars map[string]string
		want bool
	}{
		{false, nil, false},
		{true, nil, true},
		{true, map[string]string{"NO_COLOR": "1"}, false},
		{true, map[string]string{"TERM": "dumb"}, false},
	}
	for _, tt := range tests {
		out, _, _ := r.runWith(runOpts{stdoutTTY: tt.tty, vars: tt.vars})
		if got := strings.Contains(out, "\x1b["); got != tt.want {
			t.Errorf("tty=%v vars=%v: colored=%v, want %v", tt.tty, tt.vars, got, tt.want)
		}
	}
	// Porcelain and JSON are never colored.
	for _, f := range []string{"--porcelain", "--json"} {
		if out, _, _ := r.runWith(runOpts{stdoutTTY: true}, f); strings.Contains(out, "\x1b[") {
			t.Errorf("%s output is colored", f)
		}
	}
}

func TestPorcelain(t *testing.T) {
	r := fixture(t)
	out, stderr, code := r.runTool("--porcelain")
	if code != exitOK || out != "feature/merged\nfeature/old\n" {
		t.Errorf("exit %d, out %q, stderr %q", code, out, stderr)
	}
	out, stderr, _ = r.runTool("--porcelain", "--merged", "--older-than", "1y")
	if out != "" || stderr != "" {
		t.Errorf("empty porcelain: out %q, stderr %q", out, stderr)
	}
}

func TestJSON(t *testing.T) {
	r := fixture(t)
	out, stderr, code := r.runTool("--json", "--all")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Base != "main" || len(got.Branches) != 4 {
		t.Fatalf("base %q, %d branches", got.Base, len(got.Branches))
	}
	old := got.Branches[1]
	if old.Name != "feature/old" || old.Ref != "refs/heads/feature/old" || old.Status != "unmerged" ||
		old.Merged || old.Ahead != 1 || old.Behind != 3 || old.AgeDays != 120 ||
		old.Author != "Test User" || old.Email != "test@example.com" || old.Protected || old.Current {
		t.Errorf("feature/old: %+v", old)
	}
	if want := refTime.Add(-120 * day).Format(time.RFC3339); old.Date != want {
		t.Errorf("date %q, want %q", old.Date, want)
	}
	if len(old.Commit) != 40 {
		t.Errorf("commit %q", old.Commit)
	}
	base := got.Branches[3]
	if base.Name != "main" || !base.Current || !base.Protected || base.Note != "current" {
		t.Errorf("main: %+v", base)
	}

	// No matches still produce a valid, empty list.
	out, _, _ = r.runTool("--json", "--merged", "--older-than", "1y")
	if !strings.Contains(out, `"branches": []`) {
		t.Errorf("empty JSON: %s", out)
	}
}

func TestOutputUsage(t *testing.T) {
	r := fixture(t)
	if _, _, code := r.runTool("--json", "--porcelain"); code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
}

// failWriter fails every write, as a pipe does after the reader closes.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWriteBranchesReportsWriteError(t *testing.T) {
	branches := []Branch{{Name: "feature/old", Date: refTime, Author: "Ada"}}
	for _, format := range []outputFormat{formatTable, formatPorcelain, formatJSON} {
		err := writeBranches(failWriter{}, format, branches, listMeta{base: "main"}, refTime, false)
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("format %d: got %v, want closed pipe", format, err)
		}
	}
}

func TestWriteErrorExitStatus(t *testing.T) {
	r := fixture(t)
	cases := [][]string{
		nil,
		{"--porcelain"},
		{"--json"},
		{"--dry-run"},
		{"--help"},
		{"--version"},
	}
	for _, args := range cases {
		var errOut bytes.Buffer
		code := run(append([]string{"-C", r.dir}, args...), &env{
			stdin:  strings.NewReader(""),
			stdout: failWriter{},
			stderr: &errOut,
			getenv: func(string) string { return "" },
			now:    func() time.Time { return refTime },
		})
		if code != exitError {
			t.Errorf("%v: exit %d, want %d (stderr %q)", args, code, exitError, errOut.String())
		}
	}
}
