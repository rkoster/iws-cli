# iws Go Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a Go CLI that replicates the `./iws` bash script functionality for managing Incus workspace containers.

**Architecture:** Single Go binary with modular packages for CLI parsing, Incus client operations, image management, and workspace initialization. The will detect/configure Incus remotes, pull OCI images, manage container lifecycle, and initialize workspace directories and symlinks.

**Tech Stack:** Go 1.21+, lxc/incus client library, cobra CLI framework

---

### Task 1: Project Setup and Module Initialization

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `main.go`

- [ ] **Step 1: Initialize Go module**

Run: `go mod init github.com/ruben-koster/iws-cli`

Expected: Creates go.mod with module path `github.com/ruben-koster/iws-cli`

- [ ] **Step 2: Create main.go entry point**

```go
package main

import (
	"fmt"
	"os"

	"github.com/ruben-koster/iws-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n