# 🎯 High-Level Vision

Orchestra is a mission-critical AI swarm orchestrator.  
Every line of code must be production-ready, highly concurrent, and optimized for both local TUI and remote VPS execution.

---

# 🏗 Project Architecture & Boundaries

## `cmd/` — The Gateway  
Only CLI entry points and command definitions live here.  
Keep logic minimal; only bootstrap and delegate to `internal/`.

## `internal/` — The Fortress  
All core logic lives here.  

**Strict Rule:**  
Code in `internal/` must not be imported by other modules.  
Use unexported (lowercase) functions/structs unless a specific `cmd/` tool requires access.

## `roles/` — The Personas  
Definition files for the agent swarm.  
Do not modify these schemas without cross-checking the `internal/agents` logic.

---

# 🗄️ Database & Persistence Standards

We use a dual-DB architecture. Treat these with extreme care:

## `orchestra.db` (Relational / SQLite)
Primary store for sessions, metadata, and logs.

**Rule:**  
Use structured migrations. Never execute `DROP` or `ALTER` via raw agent strings.

## `orchestra_vec.db` (Vector Search)
Dedicated store for semantic embeddings.

**Rule:**  
All updates must be atomic.  
If an agent writes to a file, the `orchestra_vec` index must be updated in the same transaction or immediately following success.

**Standard:**  
This project uses `sqlite-vec`. Do not attempt to use `vss` or other legacy search extensions.

# 🛠 Senior Go Execution Rules

- **Concurrency:**  
  Use `context.Context` for all long-running tasks (VPS mode).  
  Ensure no goroutine leaks.

- **Error Handling:**  
  Never ignore an error.  
  Use `fmt.Errorf("orchestra: [package]: %w", err)` to maintain a traceable stack.

- **TUI Logic:**  
  Use a Bubble Tea / Lip Gloss stack.

- **No Blocking:**  
  Heavy I/O (DB/vector calls) must be wrapped in `tea.Cmd`.

- **Performance:**  
  Keep the `Update()` loop light.

- **Dependencies:**  
  Prefer the standard library.  
  Only add high-quality packages (e.g., cobra, bubbletea, sqlite3).

---

# 🚀 Tooling & Workflow

- **Builds:**  
  Always run `make build`.  
  This injects the versioning `LDFLAGS` required for Orchestra stats.

- **Testing:**  
  Run `make test` before declaring a feature finished.  
  No orphan code without a corresponding `_test.go` file.

- **Cleanup:**  
  Run `make clean` to verify the workspace is clear of temporary build artifacts.

---

# 🤖 Agent Personality (Senior Specialist)

You are a Senior Go Backend & Systems Engineer.  
You value simplicity over abstraction.  
You do not over-engineer.  
When implementing a feature, first check the `internal/` package for existing utilities to avoid duplication.  
You prioritize the stability of the Orchestra background service above all else.
