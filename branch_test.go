package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func loadBranches(t *testing.T, r *testRepo, base string) map[string]Branch {
	t.Helper()
	rp := &repo{git: &gitRunner{dir: r.dir}, remote: "origin"}
	ref, err := rp.resolveBase(base, false)
	if err != nil {
		t.Fatal(err)
	}
	branches, err := rp.listRefs("refs/heads/")
	if err != nil {
		t.Fatal(err)
	}
	if err := rp.classify(branches, ref, false); err != nil {
		t.Fatal(err)
	}
	m := map[string]Branch{}
	for _, b := range branches {
		m[b.Name] = b
	}
	return m
}

func TestClassify(t *testing.T) {
	r := fixture(t)
	got := loadBranches(t, r, "main")

	tests := []struct {
		name          string
		merged        bool
		ahead, behind int
		age           time.Duration
	}{
		{"main", true, 0, 0, 1 * day},
		{"feature/merged", true, 0, 2, 150 * day},
		{"feature/old", false, 1, 3, 120 * day},
		{"feature/new", false, 1, 3, 2 * day},
	}
	if len(got) != len(tests) {
		t.Fatalf("got %d branches, want %d", len(got), len(tests))
	}
	for _, tt := range tests {
		b, ok := got[tt.name]
		if !ok {
			t.Errorf("branch %s missing", tt.name)
			continue
		}
		if b.Merged != tt.merged || b.Ahead != tt.ahead || b.Behind != tt.behind {
			t.Errorf("%s: merged=%v ahead=%d behind=%d, want merged=%v ahead=%d behind=%d",
				tt.name, b.Merged, b.Ahead, b.Behind, tt.merged, tt.ahead, tt.behind)
		}
		if want := refTime.Add(-tt.age); !b.Date.Equal(want) {
			t.Errorf("%s: date %v, want %v", tt.name, b.Date, want)
		}
		if b.Author != "Test User" || b.Email != "test@example.com" {
			t.Errorf("%s: author %q <%s>", tt.name, b.Author, b.Email)
		}
	}
	if !got["main"].Current {
		t.Error("main should be the current branch")
	}
}

func TestBranchesSortedOldestFirst(t *testing.T) {
	r := fixture(t)
	out, _, code := r.runTool("--all")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []string{"feature/merged", "feature/old", "feature/new", "main"}
	if got := tableNames(out); !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
}

func TestDetectBase(t *testing.T) {
	t.Run("main", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("a", day)
		assertBase(t, r, "main")
	})
	t.Run("master", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("a", day)
		r.git("branch", "-m", "master")
		assertBase(t, r, "master")
	})
	t.Run("origin HEAD wins", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("a", day)
		r.git("branch", "trunk")
		r.git("update-ref", "refs/remotes/origin/trunk", "trunk")
		r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
		assertBase(t, r, "trunk")
	})
	t.Run("dangling origin HEAD", func(t *testing.T) {
		// What a clone is left with after the remote renames master to main.
		r := newTestRepo(t)
		r.commit("a", day)
		r.git("update-ref", "refs/remotes/origin/main", "main")
		r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
		assertBase(t, r, "main")
		if _, stderr, code := r.runTool("--all"); code != exitOK {
			t.Errorf("exit %d: %s", code, stderr)
		}
	})
	t.Run("none", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("a", day)
		r.git("branch", "-m", "trunk")
		rp := &repo{git: &gitRunner{dir: r.dir}, remote: "origin"}
		if _, err := rp.detectBase(); err == nil {
			t.Fatal("expected an error")
		}
		_, stderr, code := r.runTool()
		if code != exitError || !strings.Contains(stderr, "--base") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
}

func assertBase(t *testing.T, r *testRepo, want string) {
	t.Helper()
	rp := &repo{git: &gitRunner{dir: r.dir}, remote: "origin"}
	got, err := rp.detectBase()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("base %q, want %q", got, want)
	}
}

func TestBaseFlag(t *testing.T) {
	r := fixture(t)
	r.git("branch", "develop", "feature/old")
	got := loadBranches(t, r, "develop")
	if !got["feature/old"].Merged {
		t.Error("feature/old should be merged into develop")
	}
	if got["feature/merged"].Merged {
		t.Error("feature/merged should not be merged into develop")
	}

	_, stderr, code := r.runTool("--base", "nope")
	if code != exitError || !strings.Contains(stderr, `"nope" not found`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestBaseRemoteShorthand(t *testing.T) {
	t.Run("remote tracking wins over a same-named local branch", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("README", 30*day) // A
		r.git("branch", "feature")
		r.git("branch", "origin/feature")
		r.commit("README", 1*day) // B, main moves on
		r.git("branch", "-f", "origin/feature", "main")
		r.git("update-ref", "refs/remotes/origin/feature", "feature")

		out, stderr, code := r.runTool("--json", "--all", "--protect", "", "--base", "origin/feature")
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		var got jsonOutput
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if got.Base != "origin/feature" {
			t.Errorf("base %q, want origin/feature", got.Base)
		}
		byName := map[string]jsonBranch{}
		for _, b := range got.Branches {
			byName[b.Name] = b
		}
		// Remote origin/feature is A. Local origin/feature and main are B.
		if b := byName["feature"]; !b.Merged || b.Ahead != 0 || b.Behind != 0 || b.Note != "base" {
			t.Errorf("feature: %+v, want merged base at the remote tip", b)
		}
		if b := byName["origin/feature"]; b.Merged || b.Ahead != 1 || b.Note != "" {
			t.Errorf("local origin/feature: %+v, want an ordinary unmerged branch", b)
		}
		if b := byName["main"]; b.Merged || b.Note != "current" {
			t.Errorf("main: %+v", b)
		}
	})

	t.Run("local branch keeps its own name when it is the base", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("README", 30*day)
		r.git("branch", "feature")
		r.git("branch", "origin/feature")
		r.commit("README", 1*day)

		out, stderr, code := r.runTool("--json", "--all", "--protect", "", "--base", "origin/feature")
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		var got jsonOutput
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		notes := map[string]string{}
		for _, b := range got.Branches {
			notes[b.Name] = b.Note
		}
		if notes["origin/feature"] != "base" || notes["feature"] != "" {
			t.Errorf("notes %v", notes)
		}

		out, stderr, code = r.runTool("-n", "--force", "--protect", "", "--base", "origin/feature")
		if code != exitOK {
			t.Fatalf("dry-run exit %d: %s", code, stderr)
		}
		if strings.Contains(out, "origin/feature") || !strings.Contains(out, "feature") {
			t.Errorf("dry-run %q", out)
		}
	})

	t.Run("explicit local ref is not stripped", func(t *testing.T) {
		r := newTestRepo(t)
		r.commit("README", 10*day)
		r.git("branch", "feature")
		r.git("branch", "origin/feature")
		r.git("update-ref", "refs/remotes/origin/feature", "main")
		r.commit("README", 1*day)
		r.git("branch", "-f", "origin/feature", "main")

		out, stderr, code := r.runTool("--json", "--all", "--protect", "", "--base", "refs/heads/origin/feature")
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		var got jsonOutput
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		for _, b := range got.Branches {
			if b.Name == "origin/feature" && b.Note != "base" {
				t.Errorf("origin/feature note %q, want base", b.Note)
			}
			if b.Name == "feature" && b.Note == "base" {
				t.Errorf("feature was protected as the base")
			}
		}
	})
}

func TestNotARepo(t *testing.T) {
	r := &testRepo{t: t, dir: t.TempDir()}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(r.dir))
	_, stderr, code := r.runTool()
	if code != exitError {
		t.Errorf("exit %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "not a git repository") {
		t.Errorf("stderr %q", stderr)
	}
}
