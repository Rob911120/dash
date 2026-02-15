package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dash"

	"github.com/fsnotify/fsnotify"
)

const (
	debounceInterval = 2 * time.Second
	maxFileSize      = 64 * 1024 // 64KB
)

// projectDirs are the directories under watchDir we actually watch for embedding.
var projectDirs = []string{
	"dash",       // Go package
	"cmd",        // binaries
	"sql",        // migrations, seeds
	"scripts",    // maintenance scripts
}

// skipDirs are always skipped when walking.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
}

// embeddableExts are file extensions we generate embeddings for.
var embeddableExts = map[string]bool{
	".go": true, ".sql": true, ".md": true, ".sh": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".py": true, ".js": true, ".ts": true, ".tsx": true,
	".html": true, ".css": true, ".txt": true, ".mod": true,
}

func main() {
	watchDir := "/dash"
	if len(os.Args) > 1 {
		watchDir = os.Args[1]
	}

	// Connect to database
	db, err := dash.ConnectDB()
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(3)

	// Create router for embeddings
	router := dash.NewLLMRouter(dash.DefaultRouterConfig())

	d, err := dash.New(dash.Config{
		DB:              db,
		FileAllowedRoot: "/",
		Router:          router,
	})
	if err != nil {
		log.Fatalf("dash: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("fsnotify: %v", err)
	}
	defer watcher.Close()

	// Only watch project directories + root for top-level files
	watched := 0
	watcher.Add(watchDir)
	watched++

	for _, sub := range projectDirs {
		dir := filepath.Join(watchDir, sub)
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if skipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				watcher.Add(path)
				watched++
			}
			return nil
		})
	}

	log.Printf("dashwatch: watching %d directories under %s", watched, watchDir)

	// Debounce
	pending := &sync.Map{}
	var mu sync.Mutex
	processing := make(map[string]bool)

	go func() {
		ticker := time.NewTicker(debounceInterval)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			pending.Range(func(key, value any) bool {
				path := key.(string)
				eventTime := value.(time.Time)
				if now.Sub(eventTime) < debounceInterval {
					return true
				}
				pending.Delete(path)

				mu.Lock()
				if processing[path] {
					mu.Unlock()
					return true
				}
				processing[path] = true
				mu.Unlock()

				go func() {
					defer func() {
						mu.Lock()
						delete(processing, path)
						mu.Unlock()
					}()
					processFile(d, path)
				}()
				return true
			})
		}
	}()

	// Event loop
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if isEmbeddable(event.Name) {
					pending.Store(event.Name, time.Now())
				} else if !isEmbeddable(event.Name) && (event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
					// Log non-embeddable changes for visibility
					if !strings.Contains(event.Name, ".git") {
						log.Printf("notify: %s %s", event.Op, filepath.Base(event.Name))
					}
				}
			}
			// Auto-watch new subdirectories
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if !skipDirs[filepath.Base(event.Name)] {
						watcher.Add(event.Name)
					}
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("error: %v", err)
		}
	}
}

func processFile(d *dash.Dash, path string) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxFileSize {
		return
	}

	hash, err := fileHash(path)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fileNode, err := d.GetOrCreateNode(ctx, dash.LayerSystem, "file", path, map[string]any{
		"path": path,
	})
	if err != nil {
		log.Printf("node error %s: %v", filepath.Base(path), err)
		return
	}

	existingHash, _ := d.GetNodeContentHash(ctx, fileNode.ID)
	if existingHash == hash {
		return
	}

	content, err := readFile(path)
	if err != nil || content == "" {
		return
	}

	embedding, err := d.EmbedText(ctx, content)
	if err != nil {
		log.Printf("embed error %s: %v", filepath.Base(path), err)
		return
	}

	d.UpdateNodeEmbedding(ctx, fileNode.ID, embedding, hash)
	log.Printf("embedded: %s", path)
}

func isEmbeddable(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".log" {
		return false
	}
	return embeddableExts[ext]
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, b := range data[:min(512, len(data))] {
		if b == 0 {
			return "", nil
		}
	}
	return string(data), nil
}

