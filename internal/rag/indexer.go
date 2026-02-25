package rag

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Indexer struct {
	Store      *Store
	Embedder   *Embedder
	WorkingDir string
	Done       chan struct{}

	mu         sync.Mutex
	dirtyFiles map[string]bool
}

var ErrIndexerNotReady = errors.New("rag indexer is not initialized")

func NewIndexer(store *Store, embedder *Embedder, workingDir string) *Indexer {
	return &Indexer{
		Store:      store,
		Embedder:   embedder,
		WorkingDir: workingDir,
		Done:       make(chan struct{}),
		dirtyFiles: make(map[string]bool),
	}
}

func (i *Indexer) ensureDependencies(ctx context.Context, checkEmbedderHealth bool) error {
	if i == nil {
		return ErrIndexerNotReady
	}
	if strings.TrimSpace(i.WorkingDir) == "" {
		return errors.New("rag indexer working directory is empty")
	}
	if err := i.Store.EnsureReady(ctx); err != nil {
		return fmt.Errorf("rag store not ready: %w", err)
	}
	if i.Embedder == nil {
		return ErrEmbedderNotReady
	}
	if checkEmbedderHealth {
		healthCtx := ctx
		if healthCtx == nil {
			healthCtx = context.Background()
		}
		if _, hasDeadline := healthCtx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			healthCtx, cancel = context.WithTimeout(healthCtx, 5*time.Second)
			defer cancel()
		}
		if err := i.Embedder.EnsureReady(healthCtx); err != nil {
			return fmt.Errorf("rag embedder not ready: %w", err)
		}
	}
	return nil
}

func chunkText(text string, chunkSize, overlap int) []string {
	words := strings.Fields(text)
	var chunks []string

	if len(words) == 0 {
		return chunks
	}

	for i := 0; i < len(words); i += (chunkSize - overlap) {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

func isIgnored(path string, info fs.FileInfo) bool {
	if info.IsDir() {
		name := info.Name()
		if name == ".git" || name == "node_modules" || name == "vendor" {
			return true
		}
	} else {
		ext := filepath.Ext(path)
		if ext == ".exe" || ext == ".dll" || ext == ".so" || ext == ".dylib" || ext == ".sys" || ext == ".bin" {
			return true
		}
	}
	return false
}

func (i *Indexer) processFile(ctx context.Context, path string) error {
	if err := i.ensureDependencies(ctx, false); err != nil {
		return err
	}

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	if err := i.Store.ClearFile(ctx, path); err != nil {
		return err
	}

	chunks := chunkText(content, 512, 64)
	for idx, chunkStr := range chunks {
		embedding, err := i.Embedder.Embed(ctx, chunkStr)
		if err != nil {
			return fmt.Errorf("embed error: %w", err)
		}

		chk := Chunk{
			ID:         fmt.Sprintf("%s:%d", path, idx),
			Filepath:   path,
			ChunkIndex: idx,
			Content:    chunkStr,
		}

		if err := i.Store.SaveChunk(ctx, chk, embedding); err != nil {
			return err
		}
	}
	return nil
}

func (i *Indexer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := i.ensureDependencies(ctx, true); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	err = filepath.Walk(i.WorkingDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if isIgnored(path, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			err = watcher.Add(path)
			return err
		}

		i.mu.Lock()
		i.dirtyFiles[path] = true
		i.mu.Unlock()
		return nil
	})
	if err != nil {
		watcher.Close()
		return err
	}

	go func() {
		defer watcher.Close()
		defer close(i.Done)

		debounceTimer := time.NewTimer(2 * time.Second)
		defer debounceTimer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					info, err := os.Stat(event.Name)
					if err == nil {
						if info.IsDir() {
							_ = watcher.Add(event.Name)
						} else {
							if !isIgnored(event.Name, info) {
								i.mu.Lock()
								i.dirtyFiles[event.Name] = true
								i.mu.Unlock()
							}
						}
					}
				} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					if err := i.Store.ClearFile(ctx, event.Name); err != nil {
						fmt.Fprintf(os.Stderr, "indexer clear error for %s: %v\n", event.Name, err)
					}
				}

				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(2 * time.Second)

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)

			case <-debounceTimer.C:
				i.mu.Lock()
				filesToProcess := i.dirtyFiles
				i.dirtyFiles = make(map[string]bool)
				i.mu.Unlock()

				for f := range filesToProcess {
					if err := i.processFile(ctx, f); err != nil {
						fmt.Fprintf(os.Stderr, "indexer process error for %s: %v\n", f, err)
					}
				}
			}
		}
	}()

	return nil
}

func (i *Indexer) Query(ctx context.Context, prompt string) ([]Chunk, error) {
	if err := i.ensureDependencies(ctx, false); err != nil {
		return nil, err
	}

	emb, err := i.Embedder.Embed(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return i.Store.Search(ctx, emb, 5)
}
