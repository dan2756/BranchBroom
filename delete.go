package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// planDeletion splits the selected branches into those that will be
// deleted and those skipped because they are not merged and --force was
// not given. Squash-merged branches need --force too: git branch -d
// cannot tell that they are merged.
func planDeletion(branches []Branch, force bool) (del, skipped []Branch) {
	for _, b := range branches {
		if b.Merged || force {
			del = append(del, b)
		} else {
			skipped = append(skipped, b)
		}
	}
	return del, skipped
}

// deleteArgs returns the git arguments that delete branches.
// Names follow "--" so a branch called --force is not read as an option:
// git branch -D feature --force would delete feature, exit 0, and leave
// the branch named --force in place.
func deleteArgs(branches []Branch, force bool) []string {
	flag := "-d"
	if force {
		flag = "-D"
	}
	args := []string{"branch", flag, "--"}
	for _, b := range branches {
		args = append(args, b.Name)
	}
	return args
}

// pushDeleteArgs returns the git arguments that delete branches from a
// remote in a single push. Names follow "--" for the same reason as
// deleteArgs.
func pushDeleteArgs(remote string, branches []Branch) []string {
	args := []string{"push", remote, "--delete", "--"}
	for _, b := range branches {
		args = append(args, b.Name)
	}
	return args
}

// formatCommand renders a git invocation the way a shell would accept it.
// dir is the repository -C was given; it is included so the command deletes
// in that repository even when the shell's cwd is somewhere else.
func formatCommand(dir string, args []string) string {
	quoted := make([]string, 0, len(args)+3)
	quoted = append(quoted, "git")
	if dir != "" {
		quoted = append(quoted, "-C", shellQuote(dir))
	}
	for _, a := range args {
		quoted = append(quoted, shellQuote(a))
	}
	return strings.Join(quoted, " ")
}

// shellQuote quotes s for a POSIX shell when it contains anything beyond
// a conservative set of safe characters.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_./@%+=,:", c)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// confirm asks a yes/no question and reports whether the answer was yes.
// Anything but y or yes, including EOF, means no.
func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func pluralBranches(n int) string {
	if n == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", n)
}
