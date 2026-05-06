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

// GitPull checks for a clean working directory, stashes manual KiCad edits safely,
// and runs git pull --rebase. It restores manual edits after the pull.
func GitPull(repoPath string) error {
	if !isGitRepository(repoPath) {
		return nil
	}

	// 1. Check for manual edits made directly in KiCad
	statusOut, _ := gitCommand("-C", repoPath, "status", "--porcelain").Output()
	hasManualEdits := strings.TrimSpace(string(statusOut)) != ""

	// 2. Stash them safely before pulling
	if hasManualEdits {
		fmt.Println("    [Git] Stashing manual KiCad edits...")
		gitCommand("-C", repoPath, "stash").Run()
	}

	// 3. Pull the latest from GitHub
	out, err := gitCommand("-C", repoPath, "pull", "--rebase").CombinedOutput()

	// 4. Always restore the manual edits
	if hasManualEdits {
		fmt.Println("    [Git] Restoring manual KiCad edits...")
		popErr := gitCommand("-C", repoPath, "stash", "pop").Run()
		if popErr != nil {
			fmt.Println("    [Git Error] Conflict restoring manual edits. Favoring local changes to protect KiCad library.")
			// Force Git to keep local changes (the stashed changes) to avoid S-Expression corruption
			gitCommand("-C", repoPath, "checkout", "--ours", ".").Run()
			gitCommand("-C", repoPath, "stash", "drop").Run()
		}
	}

	if err != nil {
		return fmt.Errorf("pull failed: %s", strings.TrimSpace(string(out)))
	}

	return nil
}

// GitCommitAndPush stages all changes, commits, and pushes to remote.
// Returns (pushed=true, nil) on success.
// Returns (pushed=false, nil) if push was rejected by remote (signal for retry loop).
// Returns (false, non-nil) on hard failures (add or commit errors).
func GitCommitAndPush(repoPath, commitMessage string) (bool, error) {
	if !isGitRepository(repoPath) {
		return true, nil
	}

	if err := gitCommand("-C", repoPath, "add", ".").Run(); err != nil {
		return false, fmt.Errorf("git add failed: %w", err)
	}

	commitOut, commitErr := gitCommand("-C", repoPath, "commit", "-m", commitMessage).CombinedOutput()
	if commitErr != nil {
		// If there is nothing new to commit, it's not an error! We might just need to push an existing commit.
		if !strings.Contains(string(commitOut), "nothing to commit") {
			return false, fmt.Errorf("git commit failed: %s", strings.TrimSpace(string(commitOut)))
		}
		fmt.Println("    [Git] No new files to commit. Proceeding to push.")
	} else {
		fmt.Printf("    [Git] Committed: %q\n", commitMessage)
	}

	if err := gitCommand("-C", repoPath, "push").Run(); err != nil {
		fmt.Println("    [Git] Push rejected by remote — will retry via pull --rebase.")
		return false, nil
	}

	fmt.Println("    [Git] Successfully synchronized with remote repository.")
	return true, nil
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
