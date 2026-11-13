package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Branch is one branch together with what we learned about it.
type Branch struct {
	Name     string // short name, e.g. feature/login
	Ref      string // full ref, e.g. refs/heads/feature/login
	Commit   string
	Date     time.Time // committer date of the tip
	Author   string
	Email    string
	Current  bool   // checked out in this worktree
	Worktree string // path of the worktree that has it checked out, if any
	Merged   bool   // tip is an ancestor of the base
	Squashed bool   // not merged, but its changes are on the base (--squash)
	Ahead    int    // commits on the branch that are not on the base
	Behind   int    // commits on the base that are not on the branch

	Protected string // why the branch is protected, or ""
}

// repo wraps the git queries the tool needs.
type repo struct {
	git    *gitRunner
	remote string
}

// detectBase picks the base branch name: the target of <remote>/HEAD,
// else main, else master.
func (r *repo) detectBase() (string, error) {
	head := "refs/remotes/" + r.remote + "/HEAD"
	if out, err := r.git.output("symbolic-ref", "--quiet", head); err == nil {
		// <remote>/HEAD is left dangling when the remote renames its
		// default branch, e.g. master to main; fall back then.
		target := strings.TrimSpace(out)
		if name, ok := strings.CutPrefix(target, "refs/remotes/"+r.remote+"/"); ok && name != "" && r.git.refExists(target) {
			return name, nil
		}
	}
	for _, name := range []string{"main", "master"} {
		if r.git.refExists("refs/heads/"+name) || r.git.refExists("refs/remotes/"+r.remote+"/"+name) {
			return name, nil
		}
	}
	return "", errors.New("cannot detect the base branch (no " + r.remote + "/HEAD, main or master); use --base")
}

// resolveBase turns a base branch name into a ref to compare against,
// preferring the local branch (the remote-tracking one with remoteMode),
// then the other one, then whatever git makes of the name. HEAD and
// revisions relative to it, such as HEAD~2, are taken as they are, so
// that they do not resolve to <remote>/HEAD.
//
// A remote-tracking shorthand such as origin/main is that ref, not a
// local branch that happens to share the name. Prepending refs/heads/
// or refs/remotes/<remote>/ to the whole shorthand would look up
// refs/heads/origin/main and refs/remotes/origin/origin/main instead.
func (r *repo) resolveBase(name string, remoteMode bool) (string, error) {
	local, tracking := "refs/heads/"+name, "refs/remotes/"+r.remote+"/"+name
	candidates := []string{local, tracking, name}
	if remoteMode {
		candidates = []string{tracking, local, name}
	}
	if isHeadRevision(name) {
		candidates = []string{name}
	} else if rest, ok := strings.CutPrefix(name, r.remote+"/"); ok && rest != "" {
		candidates = []string{"refs/remotes/" + r.remote + "/" + rest, local, name}
	}
	for _, ref := range candidates {
		if r.git.refExists(ref) {
			return ref, nil
		}
	}
	return "", fmt.Errorf("base branch %q not found", name)
}

// isHeadRevision reports whether name is HEAD or a revision relative to
// it, such as HEAD~2 or HEAD^.
func isHeadRevision(name string) bool {
	if i := strings.IndexAny(name, "~^@"); i >= 0 {
		name = name[:i]
	}
	return name == "HEAD"
}

// shortRef strips refs/heads/ or refs/remotes/ from a ref for display.
func shortRef(ref string) string {
	for _, p := range []string{"refs/heads/", "refs/remotes/"} {
		if s, ok := strings.CutPrefix(ref, p); ok {
			return s
		}
	}
	return ref
}

// plainBaseName is the branch to protect for a resolved base ref.
// A symbolic ref is followed one step, so --base origin/HEAD protects the
// branch it points at (trunk, main, ...) instead of a branch named HEAD.
// Revisions such as HEAD~1 have no symbolic name and stay as given, and a
// detached HEAD stays HEAD.
//
// Only a remote-tracking ref loses its <remote>/ prefix. A local branch
// named origin/feature is not the remote-tracking branch origin/feature,
// and stripping it would protect feature while leaving the base itself
// unprotected.
func (r *repo) plainBaseName(ref string) string {
	if out, err := r.git.output("rev-parse", "--symbolic-full-name", ref); err == nil {
		if full := strings.TrimSpace(out); full != "" && full != ref && full != "HEAD" {
			ref = full
		}
	}
	if name, ok := strings.CutPrefix(ref, "refs/remotes/"+r.remote+"/"); ok && name != "" {
		return name
	}
	return shortRef(ref)
}

// checkRemote fails unless r.remote is a configured remote.
func (r *repo) checkRemote() error {
	out, err := r.git.output("remote")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(out) {
		if name == r.remote {
			return nil
		}
	}
	return fmt.Errorf("no such remote %q", r.remote)
}

// remoteCurrent returns the branch names on r.remote that belong to the
// checked-out branch: the same name, and its upstream if that lives on
// r.remote. It is empty on a detached HEAD.
func (r *repo) remoteCurrent() (map[string]bool, error) {
	out, err := r.git.output("symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return nil, nil
	}
	head := strings.TrimSpace(out)
	names := map[string]bool{strings.TrimPrefix(head, "refs/heads/"): true}
	up, err := r.git.output("for-each-ref", "--format=%(upstream)", head)
	if err != nil {
		return nil, err
	}
	if name, ok := strings.CutPrefix(strings.TrimSpace(up), "refs/remotes/"+r.remote+"/"); ok && name != "" {
		names[name] = true
	}
	return names, nil
}

// remoteBranches keeps the remote-tracking refs that the fetch refspecs
// of r.remote map from the remote's branch of the same name, which is what
// git push <remote> --delete <name> deletes. Refs that a refspec maps from
// anything else, such as pull requests fetched with
// +refs/pull/*/head:refs/remotes/origin/pr/*, are not the remote's
// branches, even though +refs/heads/*:refs/remotes/origin/* matches them too.
func (r *repo) remoteBranches(branches []Branch) ([]Branch, error) {
	specs, err := r.git.configValues("remote." + r.remote + ".fetch")
	if err != nil {
		return nil, err
	}
	var kept []Branch
	for _, b := range branches {
		branch := false
		for _, spec := range specs {
			src, ok := fetchSource(spec, b.Ref)
			if !ok {
				continue
			}
			if branch = src == "refs/heads/"+b.Name; !branch {
				break
			}
		}
		if branch {
			kept = append(kept, b)
		}
	}
	return kept, nil
}

// fetchSource returns the remote ref that the fetch refspec spec, e.g.
// +refs/heads/*:refs/remotes/origin/*, stores in the local ref dst.
func fetchSource(spec, dst string) (string, bool) {
	src, pattern, ok := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
	if !ok || strings.HasPrefix(src, "^") {
		return "", false
	}
	prefix, suffix, glob := strings.Cut(pattern, "*")
	if !glob {
		return src, pattern == dst
	}
	if len(dst) < len(prefix)+len(suffix) || !strings.HasPrefix(dst, prefix) || !strings.HasSuffix(dst, suffix) {
		return "", false
	}
	return strings.Replace(src, "*", dst[len(prefix):len(dst)-len(suffix)], 1), true
}

const refFormat = "%(refname)%00%(objectname)%00%(committerdate:unix)%00%(authorname)%00%(authoremail)%00%(HEAD)%00%(symref)%00%(worktreepath)"

// listRefs reads branch tips under prefix (e.g. refs/heads/), skipping
// symbolic refs such as refs/remotes/origin/HEAD.
func (r *repo) listRefs(prefix string) ([]Branch, error) {
	out, err := r.git.output("for-each-ref", "--format="+refFormat, prefix)
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) != 8 {
			return nil, fmt.Errorf("unexpected for-each-ref output: %q", line)
		}
		if f[6] != "" {
			continue
		}
		unix, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad commit date for %s: %v", f[0], err)
		}
		branches = append(branches, Branch{
			Name:     strings.TrimPrefix(f[0], prefix),
			Ref:      f[0],
			Commit:   f[1],
			Date:     time.Unix(unix, 0),
			Author:   f[3],
			Email:    strings.Trim(f[4], "<>"),
			Current:  f[5] == "*",
			Worktree: f[7],
		})
	}
	sort.SliceStable(branches, func(i, j int) bool {
		if !branches[i].Date.Equal(branches[j].Date) {
			return branches[i].Date.Before(branches[j].Date)
		}
		return branches[i].Name < branches[j].Name
	})
	return branches, nil
}

// landed reports whether the branch's work is on the base, by a real
// merge or (with --squash) a squash or rebase merge.
func (b Branch) landed() bool { return b.Merged || b.Squashed }

// classify fills in merge status and ahead/behind counts for each branch
// relative to base. With squash it also looks for squash-merged branches.
func (r *repo) classify(branches []Branch, base string, squash bool) error {
	for i := range branches {
		b := &branches[i]
		merged, err := r.git.check("merge-base", "--is-ancestor", b.Ref, base)
		if err != nil {
			return err
		}
		b.Merged = merged
		if b.Behind, b.Ahead, err = r.aheadBehind(base, b.Ref); err != nil {
			return err
		}
		if squash && !merged {
			if b.Squashed, err = r.squashMerged(base, b.Ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// aheadBehind counts commits only on base (left) and only on ref (right).
func (r *repo) aheadBehind(base, ref string) (left, right int, err error) {
	out, err := r.git.output("rev-list", "--left-right", "--count", base+"..."+ref)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", out)
	}
	if left, err = strconv.Atoi(f[0]); err != nil {
		return 0, 0, err
	}
	if right, err = strconv.Atoi(f[1]); err != nil {
		return 0, 0, err
	}
	return left, right, nil
}
