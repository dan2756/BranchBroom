package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// addRemote creates a bare repository, adds it as remote name, pushes the
// given branches to it and points <name>/HEAD at main. It returns the bare
// repository's path.
func (r *testRepo) addRemote(name string, branches ...string) string {
	r.t.Helper()
	bare := filepath.Join(r.t.TempDir(), name+".git")
	r.git("init", "-q", "--bare", "-b", "main", bare)
	r.git("remote", "add", name, bare)
	r.git(append([]string{"push", "-q", name}, branches...)...)
	r.git("remote", "set-head", name, "main")
	return bare
}

func bareBranches(t *testing.T, bare string) []string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", bare, "for-each-ref", "--format=%(refname:short)", "refs/heads/").CombinedOutput()
	if err != nil {
		t.Fatalf("for-each-ref: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

func remoteFixture(t *testing.T) (*testRepo, string) {
	t.Helper()
	r := fixture(t)
	bare := r.addRemote("origin", "main", "feature/merged", "feature/old", "feature/new")
	// Local-only branches must not show up in --remote listings.
	r.git("branch", "local-only", "feature/merged")
	return r, bare
}

func TestRemoteListing(t *testing.T) {
	r, _ := remoteFixture(t)
	out, stderr, code := r.runTool("--remote", "--porcelain")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := strings.Fields(out), []string{"feature/merged", "feature/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	out, _, _ = r.runTool("--remote", "--all", "--json")
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Base != "origin/main" || got.Remote != "origin" {
		t.Errorf("base %q remote %q", got.Base, got.Remote)
	}
	var names []string
	for _, b := range got.Branches {
		names = append(names, b.Name)
		if !strings.HasPrefix(b.Ref, "refs/remotes/origin/") {
			t.Errorf("ref %q", b.Ref)
		}
	}
	// origin/HEAD is a symref and is skipped.
	if want := []string{"feature/merged", "feature/old", "feature/new", "main"}; !slices.Equal(names, want) {
		t.Errorf("names %v, want %v", names, want)
	}
	if last := got.Branches[3]; last.Note != "current" {
		t.Errorf("origin/main should be protected as current, got %+v", last)
	}
}

func TestRemoteComparesAgainstRemoteBase(t *testing.T) {
	r, _ := remoteFixture(t)
	// Merge feature/old locally only: origin/main does not have it yet.
	r.merge("feature/old", 0)
	out, _, _ := r.runTool("--merged", "--porcelain")
	if got, want := strings.Fields(out), []string{"feature/merged", "local-only", "feature/old"}; !slices.Equal(got, want) {
		t.Errorf("local: got %v, want %v", got, want)
	}
	out, _, _ = r.runTool("--remote", "--merged", "--porcelain")
	if got, want := strings.Fields(out), []string{"feature/merged"}; !slices.Equal(got, want) {
		t.Errorf("remote: got %v, want %v", got, want)
	}
}

func TestRemoteCurrentIsProtected(t *testing.T) {
	r, _ := remoteFixture(t)
	r.git("switch", "-q", "feature/old")
	out, _, _ := r.runTool("--remote", "--porcelain")
	if got, want := strings.Fields(out), []string{"feature/merged"}; !slices.Equal(got, want) {
		t.Errorf("same name: got %v, want %v", got, want)
	}
	// A differently named local branch tracking origin/feature/merged.
	r.git("switch", "-q", "-c", "mine", "--track", "origin/feature/merged")
	out, _, _ = r.runTool("--remote", "--porcelain")
	if got, want := strings.Fields(out), []string{"feature/old"}; !slices.Equal(got, want) {
		t.Errorf("upstream: got %v, want %v", got, want)
	}
}

func TestRemoteDryRunAndDelete(t *testing.T) {
	r, bare := remoteFixture(t)
	out, stderr, _ := r.runTool("--remote", "-n")
	if out != fmt.Sprintf("git -C %s push origin --delete -- feature/merged\n", r.dir) {
		t.Errorf("dry run %q", out)
	}
	if !strings.Contains(stderr, "skipping feature/old: not merged into origin/main") {
		t.Errorf("stderr %q", stderr)
	}
	out, _, _ = r.runTool("--remote", "-n", "--force")
	if out != fmt.Sprintf("git -C %s push origin --delete -- feature/merged feature/old\n", r.dir) {
		t.Errorf("dry run --force %q", out)
	}

	_, stderr, code := r.runToolInput("y\n", true, "--remote", "--delete", "--force")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "Delete 2 branches from origin? [y/N]") {
		t.Errorf("prompt missing: %q", stderr)
	}
	if got, want := bareBranches(t, bare), []string{"feature/new", "main"}; !slices.Equal(got, want) {
		t.Errorf("remote branches %v, want %v", got, want)
	}
	// The remote-tracking refs are gone too; local branches are untouched.
	if out := r.git("branch", "-r"); strings.Contains(out, "feature/merged") {
		t.Errorf("stale tracking ref left behind: %q", out)
	}
	if !slices.Contains(r.branchNames(), "feature/merged") {
		t.Error("local branch was deleted")
	}
}

func TestRemoteDeleteBranchNamedLikeFlag(t *testing.T) {
	r, bare := remoteFixture(t)
	// push would treat a trailing --force as the force flag and still exit 0
	// after deleting the other branch.
	r.git("push", "-q", "origin", "main:refs/heads/--force")

	out, stderr, code := r.runTool("--remote", "-n", "--merged")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := fmt.Sprintf("git -C %s push origin --delete -- feature/merged --force\n", r.dir)
	if out != want {
		t.Errorf("dry-run %q, want %q", out, want)
	}

	if _, stderr, code = r.runTool("--remote", "--merged", "--delete", "-y"); code != exitOK {
		t.Fatalf("delete exit %d: %s", code, stderr)
	}
	if got, want := bareBranches(t, bare), []string{"feature/new", "feature/old", "main"}; !slices.Equal(got, want) {
		t.Errorf("remote branches %v, want %v", got, want)
	}
}

func TestRemoteName(t *testing.T) {
	r, _ := remoteFixture(t)
	r.addRemote("upstream", "main", "feature/old")
	out, stderr, code := r.runTool("--remote", "--remote-name", "upstream", "--porcelain")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := strings.Fields(out), []string{"feature/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	out, _, _ = r.runTool("--remote", "--remote-name=upstream", "-n", "--force")
	if out != fmt.Sprintf("git -C %s push upstream --delete -- feature/old\n", r.dir) {
		t.Errorf("dry run %q", out)
	}

	_, stderr, code = r.runTool("--remote", "--remote-name", "nope")
	if code != exitError || !strings.Contains(stderr, `no such remote "nope"`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if _, _, code := r.runTool("--remote-name", ""); code != exitUsage {
		t.Errorf("empty --remote-name: exit %d", code)
	}
}

func TestBaseRemoteTrackingBranch(t *testing.T) {
	r, _ := remoteFixture(t)
	// Local main has feature/old merged; origin/main does not.
	r.merge("feature/old", 0)
	out, stderr, code := r.runTool("--merged", "--porcelain", "--base", "origin/main")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := strings.Fields(out), []string{"feature/merged", "local-only"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	out, _, _ = r.runTool("--json", "--base", "origin/main")
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Base != "origin/main" {
		t.Errorf("base %q, want origin/main", got.Base)
	}
	for _, b := range got.Branches {
		if b.Name == "main" {
			t.Error("main should stay protected as the base")
		}
	}
}

func TestRemoteSkipsNonBranchRefs(t *testing.T) {
	r, bare := remoteFixture(t)
	// GitHub keeps refs/pull/<n>/head for every pull request, and a common
	// refspec fetches them into refs/remotes/origin/pr/<n>.
	if out, err := exec.Command("git", "--git-dir", bare, "update-ref", "refs/pull/7/head", "refs/heads/feature/merged").CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, out)
	}
	r.git("config", "--add", "remote.origin.fetch", "+refs/pull/*/head:refs/remotes/origin/pr/*")
	r.git("fetch", "-q", "origin")
	if out := r.git("branch", "-r"); !strings.Contains(out, "origin/pr/7") {
		t.Fatalf("pull request ref not fetched: %q", out)
	}

	out, _, _ := r.runTool("--remote", "--porcelain")
	if got, want := strings.Fields(out), []string{"feature/merged", "feature/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	out, _, _ = r.runTool("--remote", "-n")
	if out != fmt.Sprintf("git -C %s push origin --delete -- feature/merged\n", r.dir) {
		t.Errorf("dry run %q", out)
	}
	if _, stderr, code := r.runTool("--remote", "--merged", "--delete", "-y"); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := bareBranches(t, bare), []string{"feature/new", "feature/old", "main"}; !slices.Equal(got, want) {
		t.Errorf("remote branches %v, want %v", got, want)
	}
}

func TestFetchSource(t *testing.T) {
	tests := []struct {
		spec, dst, src string
		ok             bool
	}{
		{"+refs/heads/*:refs/remotes/origin/*", "refs/remotes/origin/feature/x", "refs/heads/feature/x", true},
		{"+refs/heads/*:refs/remotes/origin/*", "refs/remotes/upstream/x", "", false},
		{"+refs/pull/*/head:refs/remotes/origin/pr/*", "refs/remotes/origin/pr/7", "refs/pull/7/head", true},
		{"+refs/pull/*/head:refs/remotes/origin/pr/*", "refs/remotes/origin/main", "", false},
		{"refs/heads/main:refs/remotes/origin/main", "refs/remotes/origin/main", "refs/heads/main", true},
		{"refs/heads/main:refs/remotes/origin/main", "refs/remotes/origin/dev", "", false},
		{"^refs/heads/tmp/*", "refs/remotes/origin/tmp/x", "", false},
		{"refs/heads/main", "refs/remotes/origin/main", "", false},
	}
	for _, tt := range tests {
		src, ok := fetchSource(tt.spec, tt.dst)
		if ok != tt.ok || ok && src != tt.src {
			t.Errorf("fetchSource(%q, %q) = %q, %v; want %q, %v", tt.spec, tt.dst, src, ok, tt.src, tt.ok)
		}
	}
}

func TestBaseHEAD(t *testing.T) {
	r, _ := remoteFixture(t)
	// HEAD has feature/old merged; origin/HEAD (origin/main) does not.
	r.git("switch", "-q", "-c", "develop")
	r.merge("feature/old", 0)
	out, stderr, code := r.runTool("--base", "HEAD", "--merged", "--porcelain")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, want := strings.Fields(out), []string{"feature/merged", "local-only", "feature/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	out, _, _ = r.runTool("--base", "HEAD", "--json")
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Base != "HEAD" {
		t.Errorf("base %q, want HEAD", got.Base)
	}

	for name, want := range map[string]bool{
		"HEAD": true, "HEAD~2": true, "HEAD^": true, "HEAD@{1}": true,
		"origin/HEAD": false, "HEADS": false, "main~1": false,
	} {
		if got := isHeadRevision(name); got != want {
			t.Errorf("isHeadRevision(%q) = %v, want %v", name, got, want)
		}
	}
}
