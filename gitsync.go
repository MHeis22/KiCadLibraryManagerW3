package main

import (
	"fmt"
	"strings"
)

// isGitRepository returns true if the path is inside a git repository.
func isGitRepository(path string) bool {
	return gitCommand("-C", path, "rev-parse", "--git-dir").Run() == nil
}

// ValidateGitURL runs git ls-remote to confirm the URL is a reachable Git remote.
func ValidateGitURL(url string) error {
	out, err := gitCommand("ls-remote", url).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// GitClone downloads a remote repository into the target directory.
func GitClone(url string, destPath string) error {
	fmt.Printf("--> Cloning repository: %s\n", url)
	out, err := gitCommand("clone", url, destPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// GitPull checks for a clean working directory and runs git pull --rebase.
// Returns nil for non-git repos (silently skipped).
func GitPull(repoPath string) error {
	if !isGitRepository(repoPath) {
		return nil
	}

	// Abort if there are uncommitted local changes
	statusOut, _ := gitCommand("-C", repoPath, "status", "--porcelain").Output()
	if strings.TrimSpace(string(statusOut)) != "" {
		return fmt.Errorf("repo has uncommitted local changes — please resolve manually before syncing")
	}

	out, err := gitCommand("-C", repoPath, "pull", "--rebase").CombinedOutput()
	if err != nil {
		return fmt.Errorf("pull failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// GitCommitAndPush stages all changes, commits, and pushes to remote.
// Returns (pushed=true, nil) on success or when there is nothing to commit.
// Returns (pushed=false, nil) if push was rejected by remote (signal for retry).
// Returns (false, non-nil) on hard failures (add or commit errors).
// Returns (true, nil) for non-git repos.
func GitCommitAndPush(repoPath, commitMessage string) (bool, error) {
	if !isGitRepository(repoPath) {
		return true, nil
	}

	if err := gitCommand("-C", repoPath, "add", ".").Run(); err != nil {
		return false, fmt.Errorf("git add failed: %w", err)
	}

	commitOut, commitErr := gitCommand("-C", repoPath, "commit", "-m", commitMessage).CombinedOutput()
	if commitErr != nil {
		if strings.Contains(string(commitOut), "nothing to commit") {
			fmt.Println("    [Git] Nothing to commit.")
			return true, nil
		}
		return false, fmt.Errorf("git commit failed: %s", strings.TrimSpace(string(commitOut)))
	}
	fmt.Printf("    [Git] Committed: %q\n", commitMessage)

	if err := gitCommand("-C", repoPath, "push").Run(); err != nil {
		fmt.Println("    [Git] Push rejected by remote — will retry.")
		return false, nil
	}

	fmt.Println("    [Git] Successfully synchronized with remote repository.")
	return true, nil
}

// GitResetLastCommit hard-resets the working directory to HEAD~1, undoing the last commit.
func GitResetLastCommit(repoPath string) error {
	return gitCommand("-C", repoPath, "reset", "--hard", "HEAD~1").Run()
}

// GitFetchAndCheckStatus fetches from remote and returns whether the local branch
// is behind its upstream tracking ref.
func GitFetchAndCheckStatus(repoPath string) (behind bool, err error) {
	if !isGitRepository(repoPath) {
		return false, nil
	}

	// Non-destructive: updates remote tracking refs without touching the working tree
	gitCommand("-C", repoPath, "fetch", "--quiet").Run()

	localHead, err := gitCommand("-C", repoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return false, nil
	}
	remoteHead, err := gitCommand("-C", repoPath, "rev-parse", "@{u}").Output()
	if err != nil {
		return false, nil // No upstream configured — not considered behind
	}

	return strings.TrimSpace(string(localHead)) != strings.TrimSpace(string(remoteHead)), nil
}
