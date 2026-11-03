package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestDryRun(t *testing.T) {
	r := fixture(t)
	before := r.branchNames()
	tests := []struct {
		args    []string
		want    string
		skipped []string
	}{
		{[]string{"--dry-run"}, fmt.Sprintf("git -C %s branch -d -- feature/merged\n", r.dir), []string{"feature/old"}},
		{[]string{"--delete", "-n"}, fmt.Sprintf("git -C %s branch -d -- feature/merged\n", r.dir), []string{"feature/old"}},
		{[]string{"-n", "--force"}, fmt.Sprintf("git -C %s branch -D -- feature/merged feature/old\n", r.dir), nil},
		{[]string{"-n", "--unmerged", "--force"}, fmt.Sprintf("git -C %s branch -D -- feature/old feature/new\n", r.dir), nil},
		{[]string{"-n", "--unmerged"}, "", []string{"feature/old", "feature/new"}},
	}
	for _, tt := range tests {
		out, stderr, code := r.runTool(tt.args...)
		if code != exitOK {
			t.Errorf("%v: exit %d: %s", tt.args, code, stderr)
			continue
		}
		if out != tt.want {
			t.Errorf("%v: stdout %q, want %q", tt.args, out, tt.want)
		}
		for _, name := range tt.skipped {
			if !strings.Contains(stderr, "skipping "+name+": not merged into main") {
				t.Errorf("%v: no skip message for %s in %q", tt.args, name, stderr)
			}
		}
		if tt.want == "" && !strings.Contains(stderr, "nothing to delete") {
			t.Errorf("%v: stderr %q", tt.args, stderr)
		}
	}
	if after := r.branchNames(); !slices.Equal(before, after) {
		t.Errorf("dry run changed branches: %v -> %v", before, after)
	}
}

func TestDeleteBranchNamedLikeFlag(t *testing.T) {
	r := fixture(t)
	// A ref git itself will not create with `git branch`, but which can
	// still exist. Without a "--" separator, `git branch -d feature/merged
	// --force` deletes feature/merged, exits 0, and leaves --force behind.
	r.git("update-ref", "refs/heads/--force", "main")

	out, stderr, code := r.runTool("-n", "--merged")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := fmt.Sprintf("git -C %s branch -d -- feature/merged --force\n", r.dir)
	if out != want {
		t.Errorf("dry-run %q, want %q", out, want)
	}

	if _, stderr, code = r.runTool("--delete", "-y", "--merged"); code != exitOK {
		t.Fatalf("delete exit %d: %s", code, stderr)
	}
	if got, want := r.branchNames(), []string{"feature/new", "feature/old", "main"}; !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}
}

func TestDeleteMerged(t *testing.T) {
	r := fixture(t)
	out, stderr, code := r.runTool("--delete", "-y")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "Deleted branch feature/merged") {
		t.Errorf("stdout %q", out)
	}
	want := []string{"feature/new", "feature/old", "main"}
	if got := r.branchNames(); !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}
}

func TestDeleteForce(t *testing.T) {
	r := fixture(t)
	_, stderr, code := r.runTool("--delete", "--yes", "--force", "--older-than", "100d")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := []string{"feature/new", "main"}
	if got := r.branchNames(); !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}
}

func TestDeleteConfirmation(t *testing.T) {
	t.Run("no tty", func(t *testing.T) {
		r := fixture(t)
		_, stderr, code := r.runToolInput("y\n", false, "--delete")
		if code != exitUsage || !strings.Contains(stderr, "use -y") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
		if !slices.Contains(r.branchNames(), "feature/merged") {
			t.Error("branch deleted without confirmation")
		}
	})
	for _, answer := range []string{"y\n", "YES\n"} {
		t.Run("yes "+strings.TrimSpace(answer), func(t *testing.T) {
			r := fixture(t)
			out, stderr, code := r.runToolInput(answer, true, "--delete")
			if code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if !strings.Contains(stderr, "Delete 1 branch? [y/N]") {
				t.Errorf("no prompt in %q", stderr)
			}
			if !strings.HasPrefix(out, "BRANCH") || !strings.Contains(out, "feature/merged") {
				t.Errorf("branches not shown before deleting: %q", out)
			}
			if slices.Contains(r.branchNames(), "feature/merged") {
				t.Error("branch not deleted")
			}
		})
	}
	for _, answer := range []string{"", "\n", "n\n", "nope\n"} {
		t.Run("no "+strings.TrimSpace(answer), func(t *testing.T) {
			r := fixture(t)
			_, stderr, code := r.runToolInput(answer, true, "--delete")
			if code != exitOK || !strings.Contains(stderr, "aborted") {
				t.Errorf("exit %d, stderr %q", code, stderr)
			}
			if !slices.Contains(r.branchNames(), "feature/merged") {
				t.Error("branch deleted after declining")
			}
		})
	}
}

func TestDeleteNeverTouchesProtected(t *testing.T) {
	r := protectFixture(t)
	_, stderr, code := r.runTool("--delete", "-y", "--force", "--protect", "release/*")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// feature/merged is current, main is base, release/* is protected;
	// feature/new is not stale. develop and feature/old go.
	want := []string{"feature/merged", "feature/new", "main", "release/1.0", "release/2.0/rc"}
	if got := r.branchNames(); !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}

	// Even an explicit pattern cannot reach them.
	out, _, _ := r.runTool("-n", "--force", "--pattern", "*", "--protect", "")
	if strings.Contains(out, "feature/merged") || strings.Contains(out, "main") {
		t.Errorf("dry run touches protected branches: %q", out)
	}
}

func TestDeleteUsage(t *testing.T) {
	r := fixture(t)
	if _, _, code := r.runTool("--all", "--delete"); code != exitUsage {
		t.Errorf("--all --delete: exit %d, want %d", code, exitUsage)
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"feature/login":  "feature/login",
		"v1.2+build@x=y": "v1.2+build@x=y",
		"":               "''",
		"it's":           `'it'\''s'`,
		"a$b":            "'a$b'",
		"x;rm":           "'x;rm'",
		"fix(ui)":        "'fix(ui)'",
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := formatCommand("", []string{"branch", "-d", "a&b"}); got != "git branch -d 'a&b'" {
		t.Errorf("formatCommand = %q", got)
	}
	if got := formatCommand("my repo", []string{"branch", "-d", "a"}); got != "git -C 'my repo' branch -d a" {
		t.Errorf("formatCommand dir = %q", got)
	}
}

func TestDeleteFromOtherBranchHints(t *testing.T) {
	r := fixture(t)
	r.git("switch", "-q", "feature/new")
	_, stderr, code := r.runTool("--delete", "-y")
	if code != exitError {
		t.Fatalf("exit %d, want %d: %s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "hint: run from main") {
		t.Errorf("stderr %q", stderr)
	}
	if !slices.Contains(r.branchNames(), "feature/merged") {
		t.Error("feature/merged should still exist")
	}
}

func TestDeleteKeepsMachineOutputClean(t *testing.T) {
	r := fixture(t)
	out, stderr, code := r.runTool("--delete", "-y", "--porcelain")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if out != "feature/merged\n" {
		t.Errorf("stdout %q", out)
	}
	if !strings.Contains(stderr, "Deleted branch feature/merged") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestDeleteNothingKeepsMachineOutput(t *testing.T) {
	r := fixture(t)
	// Every selected branch is unmerged, so nothing is deleted.
	out, stderr, code := r.runTool("--delete", "-y", "--json", "--unmerged")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, out)
	}
	if got.Base != "main" || len(got.Branches) != 0 {
		t.Errorf("got %+v, want an empty list against main", got)
	}
	if !strings.Contains(stderr, "nothing to delete") {
		t.Errorf("stderr %q", stderr)
	}
	if out, _, _ := r.runTool("--delete", "-y", "--porcelain", "--unmerged"); out != "" {
		t.Errorf("porcelain stdout %q", out)
	}
}
