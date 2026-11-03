package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// protectFixture extends fixture with branches that default protection
// covers, and checks out feature/merged.
func protectFixture(t *testing.T) *testRepo {
	t.Helper()
	r := fixture(t)
	r.git("branch", "develop", "main~1")
	r.git("branch", "release/1.0", "feature/old")
	r.git("branch", "release/2.0/rc", "main~2")
	r.git("switch", "-q", "feature/merged")
	return r
}

func TestDefaultProtection(t *testing.T) {
	r := protectFixture(t)
	out, _, code := r.runTool()
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	// feature/merged is current, main is base, the rest match defaults.
	if got, want := tableNames(out), []string{"feature/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	out, _, _ = r.runTool("--all")
	notes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		f := strings.Fields(line)
		note := f[len(f)-1]
		if note == "current" || note == "base" || note == "protected" {
			notes[f[0]] = note
		}
	}
	want := map[string]string{
		"feature/merged": "current",
		"main":           "base",
		"develop":        "protected",
		"release/1.0":    "protected",
		"release/2.0/rc": "protected",
	}
	for name, reason := range want {
		if notes[name] != reason {
			t.Errorf("%s: note %q, want %q", name, notes[name], reason)
		}
	}
	if len(notes) != len(want) {
		t.Errorf("notes %v", notes)
	}
	if !strings.Contains(strings.SplitN(out, "\n", 2)[0], "NOTE") {
		t.Errorf("--all header has no NOTE column:\n%s", out)
	}
}

func TestProtectFlag(t *testing.T) {
	r := protectFixture(t)
	tests := []struct {
		protect string
		want    []string
	}{
		// Replaces the defaults: develop and release/* become visible.
		{"feature/*", []string{"release/2.0/rc", "develop", "release/1.0"}},
		{"feature/*,release/*", []string{"develop"}},
		{" develop , release/1.0 ", []string{"release/2.0/rc", "feature/old"}},
		// Empty keeps only current and base protected.
		{"", []string{"release/2.0/rc", "develop", "feature/old", "release/1.0"}},
	}
	for _, tt := range tests {
		out, stderr, code := r.runTool("--protect", tt.protect)
		if code != exitOK {
			t.Errorf("--protect %q: exit %d: %s", tt.protect, code, stderr)
			continue
		}
		if got := tableNames(out); !slices.Equal(got, tt.want) {
			t.Errorf("--protect %q: got %v, want %v", tt.protect, got, tt.want)
		}
	}
	if _, _, code := r.runTool("--protect", "rel[ease"); code != exitUsage {
		t.Errorf("bad --protect glob: exit %d, want %d", code, exitUsage)
	}
}

func TestProtectGitConfig(t *testing.T) {
	r := protectFixture(t)
	r.git("config", "stale-branches.protect", "release/*")
	r.git("config", "--add", "stale-branches.protect", "feature/old,nothing")

	out, _, _ := r.runTool()
	if got, want := tableNames(out), []string{"develop"}; !slices.Equal(got, want) {
		t.Errorf("config: got %v, want %v", got, want)
	}
	// The flag wins over config.
	out, _, _ = r.runTool("--protect", "develop")
	if got, want := tableNames(out), []string{"release/2.0/rc", "feature/old", "release/1.0"}; !slices.Equal(got, want) {
		t.Errorf("flag over config: got %v, want %v", got, want)
	}

	r.git("config", "--unset-all", "stale-branches.protect")
	r.git("config", "stale-branches.protect", "[oops")
	if _, stderr, code := r.runTool(); code != exitError || !strings.Contains(stderr, "stale-branches.protect") {
		t.Errorf("bad config glob: exit %d, stderr %q", code, stderr)
	}
}

func TestSymrefBaseIsProtected(t *testing.T) {
	r := newTestRepo(t)
	r.commit("README", 30*day)
	r.git("branch", "-m", "trunk")
	r.git("branch", "feature/old")
	r.git("switch", "-q", "feature/old")
	r.commit("old.txt", 20*day)
	r.git("switch", "-q", "trunk")
	r.merge("feature/old", 15*day)
	r.commit("README", 1*day)
	// topic contains trunk, so git branch -d trunk would succeed from here.
	r.git("switch", "-q", "-c", "topic")
	r.git("update-ref", "refs/remotes/origin/trunk", "trunk")
	r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	out, stderr, code := r.runTool("--json", "--all", "--base", "origin/HEAD", "--protect", "")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Base != "origin/HEAD" {
		t.Errorf("base %q, want origin/HEAD", got.Base)
	}
	notes := map[string]string{}
	for _, b := range got.Branches {
		notes[b.Name] = b.Note
	}
	if notes["trunk"] != "base" || notes["topic"] != "current" || notes["feature/old"] != "" {
		t.Errorf("notes %v", notes)
	}

	// trunk is the base even though it is not in the protect list, and
	// --force would otherwise delete it with git branch -D.
	out, stderr, code = r.runTool("-n", "--force", "--base", "origin/HEAD", "--protect", "")
	if code != exitOK {
		t.Fatalf("dry-run exit %d: %s", code, stderr)
	}
	if strings.Contains(out, "trunk") || strings.Contains(out, "topic") || !strings.Contains(out, "feature/old") {
		t.Errorf("dry-run %q", out)
	}
	if _, stderr, code = r.runTool("--delete", "-y", "--force", "--base", "origin/HEAD", "--protect", ""); code != exitOK {
		t.Fatalf("delete exit %d: %s", code, stderr)
	}
	if got, want := r.branchNames(), []string{"topic", "trunk"}; !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}
}

func TestDetachedHead(t *testing.T) {
	r := fixture(t)
	r.git("switch", "-q", "--detach", "feature/old")
	out, stderr, code := r.runTool()
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := tableNames(out), []string{"feature/merged", "feature/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWorktreeBranchesAreProtected(t *testing.T) {
	r := fixture(t)
	wt := filepath.Join(t.TempDir(), "wt")
	r.git("worktree", "add", "-q", wt, "feature/merged")

	out, stderr, code := r.runTool("--delete", "-y", "--force")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains(out, "feature/merged") {
		t.Errorf("tried to delete a branch checked out elsewhere: %q", out)
	}
	if !slices.Contains(r.branchNames(), "feature/merged") || slices.Contains(r.branchNames(), "feature/old") {
		t.Errorf("branches %v", r.branchNames())
	}

	out, _, _ = r.runTool("--json", "--all")
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, b := range got.Branches {
		if b.Name == "feature/merged" && (b.Note != "worktree" || b.Current) {
			t.Errorf("feature/merged: %+v", b)
		}
	}

	// From inside the linked worktree the roles swap.
	wtRepo := &testRepo{t: t, dir: wt}
	out, _, _ = wtRepo.runTool("--all")
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "feature/merged":
			if f[len(f)-1] != "current" {
				t.Errorf("in worktree: %q", line)
			}
		case "main":
			if f[len(f)-1] != "worktree" {
				t.Errorf("in worktree: %q", line)
			}
		}
	}
}
